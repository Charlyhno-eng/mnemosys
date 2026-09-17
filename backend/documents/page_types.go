package documents

// pageTypes is the single code-owned catalogue of page types. Page types are
// deliberately not application settings: changing this list requires a code
// change and a deployment, which keeps metadata consistent across a vault.
var pageTypes = []PageTypeDefinition{
	{ID: "general", Label: "General", Description: "General-purpose documentation.", Color: "#62a6e8", BuiltIn: true},
	{ID: "business", Label: "Business documentation", Description: "Business rules, processes and functional knowledge.", Color: "#4bc49a", BuiltIn: true},
	{ID: "technical", Label: "Technical documentation", Description: "Architecture, implementation, APIs and operations.", Color: "#a78bfa", BuiltIn: true},
	{ID: "incident", Label: "Incident documentation", Description: "Timeline, impact, root cause and corrective actions.", Color: "#f16f78", BuiltIn: true},
}

func defaultPageTypes() []PageTypeDefinition {
	return append([]PageTypeDefinition(nil), pageTypes...)
}

func containsPageType(pageType PageType) bool {
	for _, value := range pageTypes {
		if value.ID == pageType {
			return true
		}
	}
	return false
}
