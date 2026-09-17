package dev

const (
	ReviewChatHistoryEnabled     = "review_chat_history_enabled"
	PersistentReviewerHistory    = "persistent_reviewer_history"
	ReviewToolResultMaxChars     = "review_tool_result_max_chars"
	ReviewHelpResultMaxChars     = "review_help_result_max_chars"
	ReviewRecentToolResultsCount = "review_recent_tool_results_count"
	ReviewChatHistoryMaxSize     = "review_chat_history_max_size"
)

type VerifierSettings struct {
	Enabled                bool `json:"enabled"`
	ReuseHistory           bool `json:"reuseHistory"`
	ToolResultMaxChars     int  `json:"toolResultMaxChars"`
	HelpResultMaxChars     int  `json:"helpResultMaxChars"`
	RecentToolResultsCount int  `json:"recentToolResultsCount"`
	ChatHistoryMaxSize     int  `json:"chatHistoryMaxSize"`
}

func DefaultVerifierSettings() VerifierSettings {
	return VerifierSettings{
		Enabled:                true,
		ReuseHistory:           false,
		ToolResultMaxChars:     1000,
		HelpResultMaxChars:     2000,
		RecentToolResultsCount: 20,
		ChatHistoryMaxSize:     40000,
	}
}
