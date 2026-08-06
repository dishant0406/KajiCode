package agent

import "github.com/dishant0406/KajiCode/internal/kajicoderuntime"

func seedRunMessages(systemPrompt, prompt string, images []kajicoderuntime.ImageBlock, initial []kajicoderuntime.Message) []kajicoderuntime.Message {
	if len(initial) == 0 {
		return kajicoderuntime.SeedMessagesWithImages(systemPrompt, prompt, images)
	}

	messages := make([]kajicoderuntime.Message, 0, 1+len(initial)+1)
	messages = append(messages, kajicoderuntime.Message{
		Role:    kajicoderuntime.MessageRoleSystem,
		Content: systemPrompt,
	})
	for _, message := range initial {
		if message.Role == kajicoderuntime.MessageRoleSystem || emptySeedMessage(message) {
			continue
		}
		messages = append(messages, message)
	}
	messages = append(messages, kajicoderuntime.Message{
		Role:    kajicoderuntime.MessageRoleUser,
		Content: prompt,
		Images:  kajicoderuntime.CloneImageBlocks(images),
	})
	return messages
}

func emptySeedMessage(message kajicoderuntime.Message) bool {
	return message.Content == "" &&
		len(message.ToolCalls) == 0 &&
		message.ToolCallID == "" &&
		len(message.Images) == 0 &&
		len(message.Reasoning) == 0
}
