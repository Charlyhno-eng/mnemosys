package documents

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const semanticMatchThreshold = 0.16

type indexedDocument struct {
	node              Node
	document          Document
	body              string
	tokens            []string
	frequency         map[string]int
	semanticTokens    []string
	semanticFrequency map[string]int
}

func (r *repository) search(query string, mode SearchMode, scope string) ([]SearchResult, error) {
	if query == "" {
		return []SearchResult{}, nil
	}
	tree, err := r.tree()
	if err != nil {
		return nil, err
	}
	nodes := flattenSearchNodes(tree, scope)
	indexed := make([]indexedDocument, 0, len(nodes))
	for _, node := range nodes {
		document := Document{}
		body := ""
		var semanticTokens []string
		var semanticFrequency map[string]int
		if node.Type == "document" {
			var err error
			document, err = r.get(node.Path)
			if err != nil {
				return nil, err
			}
			body = markdownBody(document.Content)
			semanticTokens = searchTokens(strings.Join([]string{node.Name, node.Title, body}, " "))
			semanticFrequency = tokenFrequency(semanticTokens)
		}
		tokens := searchTokens(strings.Join([]string{node.Name, node.Path, node.Title, string(node.PageType), node.Owner, node.Application, body}, " "))
		indexed = append(indexed, indexedDocument{node: node, document: document, body: body, tokens: tokens, frequency: tokenFrequency(tokens), semanticTokens: semanticTokens, semanticFrequency: semanticFrequency})
	}

	queryTokens := searchTokens(query)
	if len(queryTokens) == 0 {
		return []SearchResult{}, nil
	}
	semanticScores := make([]float64, len(indexed))
	if mode == SearchHybrid {
		semanticScores = semanticSearchScores(indexed, queryTokens)
	}
	results := make([]SearchResult, 0)
	for index, candidate := range indexed {
		lexical := lexicalSearchScore(candidate, query, queryTokens)
		semantic := semanticScores[index]
		if lexical <= 0 && semantic < semanticMatchThreshold {
			continue
		}
		matchTypes := make([]string, 0, 2)
		if lexical > 0 {
			matchTypes = append(matchTypes, "lexical")
		}
		if mode == SearchHybrid && semantic >= semanticMatchThreshold {
			matchTypes = append(matchTypes, "semantic")
		}
		title := candidate.node.Title
		if title == "" {
			title = strings.TrimSuffix(candidate.node.Name, ".md")
		}
		results = append(results, SearchResult{
			Path:       candidate.node.Path,
			Type:       candidate.node.Type,
			Name:       candidate.node.Name,
			Title:      title,
			PageType:   candidate.document.PageType,
			Snippet:    searchSnippet(candidate.body, query, queryTokens),
			Score:      lexical + semantic*4,
			MatchTypes: matchTypes,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Path < results[j].Path
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > 50 {
		results = results[:50]
	}
	return results, nil
}

func flattenSearchNodes(nodes []Node, scope string) []Node {
	result := make([]Node, 0)
	for _, node := range nodes {
		if scope == "" || node.Path == scope || strings.HasPrefix(node.Path, scope+"/") {
			result = append(result, node)
		}
		if node.Type == "directory" {
			result = append(result, flattenSearchNodes(node.Children, scope)...)
		}
	}
	return result
}

func lexicalSearchScore(candidate indexedDocument, rawQuery string, queryTokens []string) float64 {
	title := strings.ToLower(strings.TrimSuffix(candidate.node.Name, ".md") + " " + candidate.node.Title)
	path := strings.ToLower(candidate.node.Path)
	metadata := strings.ToLower(strings.Join([]string{string(candidate.node.PageType), candidate.node.Owner, candidate.node.Application}, " "))
	body := strings.ToLower(candidate.body)
	phrase := strings.ToLower(strings.TrimSpace(rawQuery))
	score := 0.0
	if phrase != "" {
		if strings.Contains(title, phrase) {
			score += 12
		}
		if strings.Contains(path, phrase) {
			score += 8
		}
		if strings.Contains(body, phrase) {
			score += 4
		}
	}
	for _, token := range queryTokens {
		score += float64(strings.Count(title, token))*6 + float64(strings.Count(path, token))*4
		score += float64(strings.Count(metadata, token))*3 + float64(candidate.frequency[token])
	}
	return score
}

// semanticSearchScores derives a lightweight distributional vector from the
// vault itself: terms found around query concepts expand the query, then cosine
// similarity ranks documents sharing that context. Lexical scores remain intact.
func semanticSearchScores(documents []indexedDocument, queryTokens []string) []float64 {
	scores := make([]float64, len(documents))
	if len(documents) == 0 {
		return scores
	}
	documentFrequency := make(map[string]int)
	semanticDocumentCount := 0
	for _, document := range documents {
		if len(document.semanticFrequency) > 0 {
			semanticDocumentCount++
		}
		for token := range document.semanticFrequency {
			documentFrequency[token]++
		}
	}
	idf := func(token string) float64 {
		return math.Log(1+float64(semanticDocumentCount)/float64(1+documentFrequency[token])) + 1
	}
	queryVector := make(map[string]float64)
	for _, queryToken := range queryTokens {
		queryVector[queryToken] += 2 * idf(queryToken)
		for _, document := range documents {
			queryCount := document.semanticFrequency[queryToken]
			if queryCount == 0 {
				continue
			}
			scale := float64(queryCount) / math.Sqrt(float64(max(1, len(document.semanticTokens))))
			for token, count := range document.semanticFrequency {
				if token == queryToken {
					continue
				}
				queryVector[token] += math.Min(float64(count), 3) * idf(token) * scale
			}
		}
	}
	queryNorm := vectorNorm(queryVector)
	if queryNorm == 0 {
		return scores
	}
	for index, document := range documents {
		dot, norm := 0.0, 0.0
		for token, count := range document.semanticFrequency {
			weight := (1 + math.Log(float64(count))) * idf(token)
			dot += queryVector[token] * weight
			norm += weight * weight
		}
		if norm > 0 {
			scores[index] = dot / (queryNorm * math.Sqrt(norm))
		}
	}
	return scores
}

func vectorNorm(vector map[string]float64) float64 {
	sum := 0.0
	for _, value := range vector {
		sum += value * value
	}
	return math.Sqrt(sum)
}

func tokenFrequency(tokens []string) map[string]int {
	frequency := make(map[string]int)
	for _, token := range tokens {
		frequency[token]++
	}
	return frequency
}

func searchTokens(value string) []string {
	tokens := make([]string, 0)
	var current strings.Builder
	flush := func() {
		token := searchStem(strings.ToLower(current.String()))
		current.Reset()
		if len([]rune(token)) < 2 || searchStopWords[token] {
			return
		}
		tokens = append(tokens, token)
	}
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			current.WriteRune(character)
		} else if current.Len() > 0 {
			flush()
		}
	}
	if current.Len() > 0 {
		flush()
	}
	return tokens
}

func searchStem(token string) string {
	for _, suffix := range []string{"ments", "ment", "ations", "ation", "ingly", "edly", "ing", "ies", "ed", "es", "s"} {
		if strings.HasSuffix(token, suffix) && len([]rune(token)) > len([]rune(suffix))+3 {
			if suffix == "ies" {
				return strings.TrimSuffix(token, suffix) + "y"
			}
			return strings.TrimSuffix(token, suffix)
		}
	}
	return token
}

func markdownBody(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	if closing := strings.Index(content[4:], "\n---"); closing >= 0 {
		return strings.TrimSpace(content[closing+8:])
	}
	return content
}

func searchSnippet(body, query string, queryTokens []string) string {
	lines := strings.Split(body, "\n")
	selected := ""
	for _, line := range lines {
		plain := strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
		if plain == "" {
			continue
		}
		if selected == "" {
			selected = plain
		}
		lower := strings.ToLower(plain)
		if strings.Contains(lower, strings.ToLower(strings.TrimSpace(query))) {
			selected = plain
			break
		}
		for _, token := range queryTokens {
			if strings.Contains(lower, token) {
				selected = plain
				break
			}
		}
	}
	runes := []rune(selected)
	if len(runes) > 220 {
		return string(runes[:220]) + "…"
	}
	return selected
}

var searchStopWords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true,
	"de": true, "des": true, "du": true, "et": true, "for": true, "from": true, "in": true, "is": true,
	"la": true, "le": true, "les": true, "of": true, "on": true, "or": true, "the": true, "to": true,
	"un": true, "une": true, "with": true,
}
