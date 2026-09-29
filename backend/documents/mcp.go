package documents

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strings"
)

const mcpVersion = "2025-06-18"

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func mcpTools() []mcpTool {
	tool := func(name, description string, required []string, fields ...string) mcpTool {
		properties := make(map[string]any)
		for _, field := range fields {
			properties[field] = map[string]string{"type": "string"}
		}
		return mcpTool{name, description, map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}}
	}
	return []mcpTool{
		tool("search", "Search page/folder names, metadata and content. mode: names (file/folder paths only), lexical (default), or hybrid (lexical plus semantic ranking); scope: optional vault-relative folder.", []string{"query"}, "query", "mode", "scope"),
		tool("read", "Read a Markdown page including frontmatter and its revision.", []string{"path"}, "path"),
		tool("explore", "Return the recursive page/folder tree, optionally below a vault-relative folder path.", []string{}, "path"),
		tool("create", "Create a page or folder. type: document or directory. Parents must exist. pageType defaults to general. Requires AI create permission.", []string{"path", "type"}, "path", "type", "content", "pageType"),
		tool("delete", "Delete a page or folder recursively. Requires AI delete permission.", []string{"path"}, "path"),
		tool("update", "Propose replacement Markdown for human review; does not merge. Requires AI view/edit, ai_editable and the revision returned by read.", []string{"path", "content", "baseRevision"}, "path", "content", "baseRevision"),
		tool("link", "Propose an ordinary wiki link appended to a page. target is an existing vault-relative page/folder path. Requires AI view/edit, ai_editable and baseRevision.", []string{"path", "target", "baseRevision"}, "path", "target", "baseRevision", "label"),
		tool("validate", "Check a stored page or supplied Markdown for valid identity, page type and resolvable wiki links. Read-only; does not approve proposals.", []string{"path"}, "path", "content"),
	}
}

// This local, stateless Streamable HTTP endpoint always acts as AI. It never
// borrows the browser's active Human profile or exposes settings/merge tools.
func (s *Service) serveMCP(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	requestHost := r.Host
	if h, _, e := net.SplitHostPort(requestHost); e == nil {
		requestHost = h
	}
	ip := net.ParseIP(host)
	hostIP := net.ParseIP(requestHost)
	if err != nil || ip == nil || !ip.IsLoopback() || (requestHost != "localhost" && (hostIP == nil || !hostIP.IsLoopback())) {
		http.Error(w, "MCP is available on loopback only", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
		http.Error(w, "Origin is not allowed", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "Streaming and session deletion are not supported", http.StatusMethodNotAllowed)
		return
	}
	if version := r.Header.Get("MCP-Protocol-Version"); version != "" && version != mcpVersion {
		http.Error(w, "Unsupported MCP protocol version", http.StatusBadRequest)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		http.Error(w, "Expected application/json", http.StatusUnsupportedMediaType)
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "application/json") || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		http.Error(w, "Accept must include application/json and text/event-stream", http.StatusNotAcceptable)
		return
	}
	var request struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	rpcError := func(id json.RawMessage, code int, message string) {
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(&request); err != nil {
		rpcError(nil, -32700, "Invalid JSON request")
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		rpcError(nil, -32700, "Expected one JSON request")
		return
	}
	if request.JSONRPC != "2.0" || request.Method == "" {
		rpcError(nil, -32600, "Invalid request")
		return
	}
	if len(request.ID) == 0 {
		// Notifications must never execute tools or other mutations.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var id any
	if json.Unmarshal(request.ID, &id) != nil {
		rpcError(nil, -32600, "Invalid id")
		return
	}
	switch id.(type) {
	case string, float64:
	default:
		rpcError(nil, -32600, "Invalid id")
		return
	}
	var result any
	switch request.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ClientInfo      map[string]any `json:"clientInfo"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.ProtocolVersion == "" || params.Capabilities == nil || params.ClientInfo == nil {
			rpcError(request.ID, -32602, "Invalid initialize parameters")
			return
		}
		result = map[string]any{"protocolVersion": mcpVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "mnemosys", "version": "0.1.0"}, "instructions": "All calls use AI permissions. update/link return proposals requiring human review in Mnemosys. Read revisions before editing."}
	case "ping":
		result = map[string]any{}
	case "tools/list":
		result = map[string]any{"tools": mcpTools()}
	case "tools/call":
		var params struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(request.Params, &params) != nil {
			rpcError(request.ID, -32602, "Invalid tool call")
			return
		}
		var definition *mcpTool
		for _, tool := range mcpTools() {
			if tool.Name == params.Name {
				copy := tool
				definition = &copy
				break
			}
		}
		if definition == nil {
			rpcError(request.ID, -32602, "Unknown tool")
			return
		}
		arguments := make(map[string]string)
		properties := definition.InputSchema["properties"].(map[string]any)
		for key, raw := range params.Arguments {
			var value string
			if _, ok := properties[key]; !ok || string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
				rpcError(request.ID, -32602, "Invalid argument: "+key)
				return
			}
			arguments[key] = value
		}
		for _, key := range definition.InputSchema["required"].([]string) {
			if value, ok := arguments[key]; !ok || (key != "content" && strings.TrimSpace(value) == "") {
				rpcError(request.ID, -32602, "Missing argument: "+key)
				return
			}
		}
		value, callErr := s.callMCPToolForProfile(r.Header.Get("X-Mnemosys-Profile-ID"), params.Name, arguments)
		if callErr != nil {
			value = map[string]any{"error": callErr.Error()}
			var conflict *EditConflictError
			if errors.As(callErr, &conflict) {
				value = map[string]any{"error": callErr.Error(), "currentDocument": conflict.Current}
			}
		}
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			rpcError(request.ID, -32603, "Unable to encode tool result")
			return
		}
		result = map[string]any{"content": []any{map[string]string{"type": "text", "text": string(encoded)}}, "isError": callErr != nil}
	default:
		rpcError(request.ID, -32601, "Method not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
}

func (s *Service) callMCPTool(name string, args map[string]string) (any, error) {
	return s.callMCPToolForProfile("", name, args)
}

func (s *Service) callMCPToolForProfile(profileID, name string, args map[string]string) (any, error) {
	// Keep permission checks, reads, revision checks and proposal creation atomic.
	s.mu.Lock()
	defer s.mu.Unlock()
	permissions := s.aiPermissions
	owner := "AI"
	if profileID != "" {
		found := false
		for _, entry := range s.profiles {
			if entry.ID == profileID && entry.Type == ProfileAI {
				permissions, owner, found = entry.Permissions, profileName(entry.ProfileSettings), true
				break
			}
		}
		if !found {
			return nil, ErrForbidden
		}
	} else if s.activeProfileID != "legacy" {
		permissions = Permissions{}
	}
	if !permissions.View {
		return nil, ErrForbidden
	}
	path := args["path"]
	switch name {
	case "search":
		query, mode, scope := strings.TrimSpace(args["query"]), SearchMode(args["mode"]), args["scope"]
		if mode == "" {
			mode = SearchLexical
		}
		if len(query) > 200 || (mode != SearchNames && mode != SearchLexical && mode != SearchHybrid) {
			return nil, ErrInvalidSearch
		}
		if scope != "" {
			if err := validatePath(scope, false); err != nil {
				return nil, err
			}
		}
		results, err := s.repository.search(query, mode, scope)
		return SearchResponse{Query: query, Mode: mode, Results: results}, err
	case "read":
		return s.repository.get(path)
	case "explore":
		if path == "" {
			return s.repository.tree()
		}
		if err := validatePath(path, false); err != nil {
			return nil, err
		}
		if err := s.repository.verifyExistingPath(path, true); err != nil {
			return nil, err
		}
		return s.repository.readDirectory(s.repository.absolute(path), path)
	case "create":
		if !permissions.Create {
			return nil, ErrForbidden
		}
		if aiEditableFromContent(args["content"]) {
			return nil, fmt.Errorf("%w: only a human can enable ai_editable", ErrForbidden)
		}
		pageType := PageType(args["pageType"])
		if pageType == "" {
			pageType = PageTypeGeneral
		}
		if !containsPageType(pageType) {
			return nil, ErrInvalidPageType
		}
		err := s.repository.create(CreateInput{Path: path, Type: args["type"], PageType: pageType, Content: args["content"], Owner: owner, ModifiedBy: ProfileAI})
		return map[string]string{"path": path}, err
	case "delete":
		if !permissions.Delete {
			return nil, ErrForbidden
		}
		if err := s.repository.remove(path); err != nil {
			return nil, err
		}
		return map[string]string{"path": path}, nil
	case "update", "link":
		if !permissions.Edit {
			return nil, ErrForbidden
		}
		document, err := s.repository.get(path)
		if err != nil {
			return nil, err
		}
		if !document.AIEditable {
			return nil, ErrForbidden
		}
		if args["baseRevision"] == "" {
			return nil, ErrRevisionRequired
		}
		if document.Revision != args["baseRevision"] {
			return nil, &EditConflictError{Current: document}
		}
		content := args["content"]
		if name == "link" {
			target := args["target"]
			if err := validatePath(target, false); err != nil {
				return nil, err
			}
			if target == path {
				return nil, fmt.Errorf("cannot link a page to itself")
			}
			if err := s.repository.verifyExistingPath(target, false); err != nil {
				return nil, err
			}
			if strings.HasSuffix(target, ".md") {
				linked, err := s.repository.get(target)
				if err != nil {
					return nil, err
				}
				target = "id:" + linked.ID
			} else if err := s.repository.verifyExistingPath(target, true); err != nil {
				return nil, err
			}
			label := args["label"]
			if label == "" {
				label = args["target"]
			}
			if strings.ContainsAny(target+label, "[]|\r\n#") {
				return nil, fmt.Errorf("target or label cannot contain wiki delimiters")
			}
			content = strings.TrimRight(document.Content, "\r\n") + "\n\n[[" + target + "|" + label + "]]\n"
		}
		if !containsPageType(pageTypeFromContent(content)) {
			return nil, ErrInvalidPageType
		}
		proposal, err := s.createProposalLocked(path, document.Content, content)
		return UpdateResult{Path: path, Proposal: &proposal}, err
	case "validate":
		document, err := s.repository.get(path)
		if err != nil {
			return nil, err
		}
		content, provided := args["content"]
		if !provided {
			content = document.Content
		}
		issues := make([]string, 0)
		if documentIDFromContent(content) != document.ID {
			issues = append(issues, "Missing or changed document UUID")
		}
		if frontmatterValue(content, frontmatterPageTypePattern) == "" || !containsPageType(pageTypeFromContent(content)) {
			issues = append(issues, "Missing or invalid page_type")
		}
		graph, err := s.repository.graph()
		if err != nil {
			return nil, err
		}
		byPath := make(map[string]GraphNode)
		aliases := make(map[string][]string)
		for _, node := range graph.Nodes {
			byPath[node.ID] = node
			keys := make(map[string]bool)
			for _, key := range []string{node.ID, strings.TrimSuffix(node.ID, ".md"), node.Name, filepath.Base(node.ID)} {
				keys[strings.ToLower(key)] = true
			}
			if node.DocumentID != "" {
				keys[node.DocumentID] = true
				keys["id:"+node.DocumentID] = true
			}
			for key := range keys {
				key = strings.ToLower(key)
				aliases[key] = append(aliases[key], node.ID)
			}
		}
		for _, match := range wikiLinkPattern.FindAllStringSubmatch(markdownBody(content), -1) {
			target := parseWikiLink(match[1])
			if resolveGraphTarget(target, byPath, aliases) == "" {
				issues = append(issues, "Unresolved or ambiguous wiki link: "+target)
			}
		}
		return map[string]any{"path": path, "valid": len(issues) == 0, "issues": issues}, nil
	}
	return nil, fmt.Errorf("unknown tool")
}
