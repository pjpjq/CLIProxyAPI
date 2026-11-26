package helps

import "github.com/MoeForge/moego/ojson"

// openAIStyleUsageNode also accepts providers that wrap OpenAI usage in data/result envelopes.
func openAIStyleUsageNode(root ojson.Result) ojson.Result {
	if usageNode := root.Get("usage"); hasOpenAIStyleUsageTokenFields(usageNode) {
		return usageNode
	}
	if usageNode := root.Get("response.usage"); hasOpenAIStyleUsageTokenFields(usageNode) {
		return usageNode
	}
	if usageNode := root.Get("data.usage"); hasOpenAIStyleUsageTokenFields(usageNode) {
		return usageNode
	}
	if usageNode := root.Get("result.usage"); hasOpenAIStyleUsageTokenFields(usageNode) {
		return usageNode
	}
	if usageNode := root.Get("usage"); usageNode.Exists() {
		return usageNode
	}
	return root.Get("response.usage")
}
