package dev

import (
	"sidekick/persisted_ai"
)

// HydrateActivityFunc returns the Hydrate activity method for use with persisted_ai.ExecuteChatStream.
// This allows the persisted_ai package to call the Hydrate activity without importing dev.
func HydrateActivityFunc() persisted_ai.ChatHistoryHydrateActivity {
	var cha *ChatHistoryActivities
	return cha.Hydrate
}

// Ensure ChatHistoryActivities.Hydrate matches the expected signature
var _ persisted_ai.ChatHistoryHydrateActivity = (*ChatHistoryActivities)(nil).Hydrate
