package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"sidekick/llm2"
	"sidekick/persisted_ai"

	"github.com/gin-gonic/gin"
)

type HydrateChatHistoryRequest struct {
	Refs []persisted_ai.MessageRef `json:"refs"`
}

type HydrateChatHistoryResponse struct {
	Messages []llm2.Message `json:"messages"`
}

func (ctrl *Controller) HydrateChatHistoryHandler(c *gin.Context) {
	workspaceId := c.Param("workspaceId")
	flowId := c.Param("id")

	if workspaceId == "" || flowId == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID and Flow ID are required"})
		return
	}

	var req HydrateChatHistoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Collect all block keys in order, building prefixed storage keys
	var allBlockKeys []string
	var allStorageKeys []string
	for _, ref := range req.Refs {
		for _, blockKey := range ref.BlockKeys {
			allBlockKeys = append(allBlockKeys, blockKey)
			allStorageKeys = append(allStorageKeys, fmt.Sprintf("%s:msg:%s", flowId, blockKey))
		}
	}

	ctx := c.Request.Context()

	// Fetch all blocks in a single MGet call
	var blockData [][]byte
	var err error
	if len(allStorageKeys) > 0 {
		blockData, err = ctrl.service.MGet(ctx, workspaceId, allStorageKeys)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch content blocks"})
			return
		}
	}

	// Build blockKey -> raw bytes map
	blockDataMap := make(map[string][]byte)
	for i, blockKey := range allBlockKeys {
		if i < len(blockData) && blockData[i] != nil {
			blockDataMap[blockKey] = blockData[i]
		}
	}

	// Construct messages in refs order, with error placeholders for missing/malformed blocks
	messages := make([]llm2.Message, 0, len(req.Refs))
	for _, ref := range req.Refs {
		var content []llm2.ContentBlock
		for _, blockKey := range ref.BlockKeys {
			raw, ok := blockDataMap[blockKey]
			if !ok {
				content = append(content, llm2.ContentBlock{
					Id:   blockKey,
					Type: llm2.ContentBlockTypeText,
					Text: fmt.Sprintf("[hydrate error: missing block %s]", blockKey),
				})
				continue
			}

			var block llm2.ContentBlock
			if err := json.Unmarshal(raw, &block); err != nil {
				content = append(content, llm2.ContentBlock{
					Id:   blockKey,
					Type: llm2.ContentBlockTypeText,
					Text: fmt.Sprintf("[hydrate error: malformed block %s: %v]", blockKey, err),
				})
				continue
			}

			content = append(content, block)
		}
		messages = append(messages, llm2.Message{
			Role:    llm2.Role(ref.Role),
			Content: content,
		})
	}

	c.JSON(http.StatusOK, HydrateChatHistoryResponse{Messages: messages})
}
