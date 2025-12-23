package api

import (
	"encoding/json"
	"net/http"

	"sidekick/common"
	"sidekick/llm2"

	"github.com/gin-gonic/gin"
	"github.com/kelindar/binary"
)

type HydrateChatHistoryRequest struct {
	Refs []common.MessageRef `json:"refs"`
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

	// Validate that all refs have matching flowId
	for _, ref := range req.Refs {
		if ref.FlowId != "" && ref.FlowId != flowId {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Mismatched flowId in ref"})
			return
		}
	}

	// Collect all block IDs in order
	var allBlockIds []string
	for _, ref := range req.Refs {
		allBlockIds = append(allBlockIds, ref.BlockIds...)
	}

	ctx := c.Request.Context()

	// Fetch all blocks in a single MGet call
	var blockData [][]byte
	var err error
	if len(allBlockIds) > 0 {
		blockData, err = ctrl.service.MGet(ctx, workspaceId, allBlockIds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch content blocks"})
			return
		}
	}

	// Build blockId -> ContentBlock map
	blockMap := make(map[string]llm2.ContentBlock)
	for i, blockId := range allBlockIds {
		if i < len(blockData) && blockData[i] != nil {
			// First binary-unmarshal to get the original []byte that was stored
			var jsonBytes []byte
			if err := binary.Unmarshal(blockData[i], &jsonBytes); err != nil {
				continue
			}
			// Then JSON-unmarshal the content block
			var block llm2.ContentBlock
			if err := json.Unmarshal(jsonBytes, &block); err == nil {
				blockMap[blockId] = block
			}
		}
	}

	// Construct messages in refs order, omitting missing blocks
	messages := make([]llm2.Message, 0, len(req.Refs))
	for _, ref := range req.Refs {
		var content []llm2.ContentBlock
		for _, blockId := range ref.BlockIds {
			if block, ok := blockMap[blockId]; ok {
				content = append(content, block)
			}
		}
		messages = append(messages, llm2.Message{
			Role:    llm2.Role(ref.Role),
			Content: content,
		})
	}

	c.JSON(http.StatusOK, HydrateChatHistoryResponse{Messages: messages})
}
