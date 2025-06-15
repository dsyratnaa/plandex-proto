package model

import (
	"context"
	"encoding/json"
	"fmt"
	"plandex-server/db"
	"plandex-server/model/prompts"
	"plandex-server/types"
	"plandex-server/utils"

	shared "plandex-shared"

	"github.com/sashabaranov/go-openai"
)

func GenPlanName(
	auth *types.ServerAuth,
	plan *db.Plan,
	settings *shared.PlanSettings,
	clients map[string]ClientInfo,
	planContent string,
	sessionId string,
	ctx context.Context,
) (string, error) {
	config := settings.ModelPack.Namer

	var tools []openai.Tool
	var toolChoice *openai.ToolChoice

	var sysPrompt string
	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		sysPrompt = prompts.SysPlanNameXml
	} else {
		sysPrompt = prompts.SysPlanName
		tools = []openai.Tool{
			{
				Type:     "function",
				Function: &prompts.PlanNameFn,
			},
		}
		choice := openai.ToolChoice{
			Type: "function",
			Function: openai.ToolFunction{
				Name: prompts.PlanNameFn.Name,
			},
		}
		toolChoice = &choice
	}

	prompt := prompts.GetPlanNamePrompt(sysPrompt, planContent)

	messages := []types.ExtendedChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: prompt,
				},
			},
		},
		{
			Role: openai.ChatMessageRoleUser,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: planContent, // Use the planContent for the user message
				},
			},
		},
	}

	modelRes, err := ModelRequest(ctx, ModelRequestParams{
		Clients:     clients,
		Auth:        auth,
		Plan:        plan,
		ModelConfig: &config,
		Purpose:     "Plan name",
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  toolChoice,
		SessionId:   sessionId,
	})

	if err != nil {
		fmt.Printf("Error during plan name model call: %v\n", err)
		return "", err
	}

	var planName string
	content := modelRes.Content

	// Enhanced debugging for function call parsing
	fmt.Printf("GenPlanName: Raw content from model: '%s'\n", content)
	fmt.Printf("GenPlanName: Content length: %d\n", len(content))
	fmt.Printf("GenPlanName: Content bytes: %v\n", []byte(content))

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		planName = utils.GetXMLContent(content, "planName")
		if planName == "" {
			return "", fmt.Errorf("No planName tag found in XML response")
		}
	} else {
		if content == "" {
			fmt.Println("GenPlanName: ERROR - no namePlan function call found in response - content is empty")
			return "", fmt.Errorf("No namePlan function call found in response. The model failed to generate a valid response.")
		}

		fmt.Printf("GenPlanName: Attempting to unmarshal content: '%s'\n", content)
		var nameRes prompts.PlanNameRes
		err = json.Unmarshal([]byte(content), &nameRes)
		if err != nil {
			fmt.Printf("GenPlanName: Error unmarshalling plan description response: %v\n", err)
			fmt.Printf("GenPlanName: Failed content was: '%s'\n", content)
			return "", err
		}
		fmt.Printf("GenPlanName: Successfully unmarshaled, planName: '%s'\n", nameRes.PlanName)
		planName = nameRes.PlanName
	}

	return planName, nil
}

func GenPipedDataName(
	ctx context.Context,
	auth *types.ServerAuth,
	plan *db.Plan,
	settings *shared.PlanSettings,
	clients map[string]ClientInfo,
	pipedContent string,
	sessionId string,
) (string, error) {
	config := settings.ModelPack.Namer

	var sysPrompt string
	var tools []openai.Tool
	var toolChoice *openai.ToolChoice

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		sysPrompt = prompts.SysPipedDataNameXml
	} else {
		sysPrompt = prompts.SysPipedDataName
		tools = []openai.Tool{
			{
				Type:     "function",
				Function: &prompts.PipedDataNameFn,
			},
		}
		choice := openai.ToolChoice{
			Type: "function",
			Function: openai.ToolFunction{
				Name: prompts.PipedDataNameFn.Name,
			},
		}
		toolChoice = &choice
	}

	prompt := prompts.GetPipedDataNamePrompt(sysPrompt, pipedContent)

	messages := []types.ExtendedChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: prompt,
				},
			},
		},
	}

	modelRes, err := ModelRequest(ctx, ModelRequestParams{
		Clients:     clients,
		Auth:        auth,
		Plan:        plan,
		ModelConfig: &config,
		Purpose:     "Piped data name",
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  toolChoice,
		SessionId:   sessionId,
	})

	if err != nil {
		fmt.Printf("Error during piped data name model call: %v\n", err)
		return "", err
	}

	var name string
	content := modelRes.Content

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		name = utils.GetXMLContent(content, "name")
		if name == "" {
			return "", fmt.Errorf("No name tag found in XML response")
		}
	} else {
		if content == "" {
			fmt.Println("no namePipedData function call found in response")
			return "", fmt.Errorf("No namePipedData function call found in response. The model failed to generate a valid response.")
		}

		var nameRes prompts.PipedDataNameRes
		err = json.Unmarshal([]byte(content), &nameRes)
		if err != nil {
			fmt.Printf("Error unmarshalling piped data name response: %v\n", err)
			return "", err
		}
		name = nameRes.Name
	}

	return name, nil
}

func GenNoteName(
	ctx context.Context,
	auth *types.ServerAuth,
	plan *db.Plan,
	settings *shared.PlanSettings,
	clients map[string]ClientInfo,
	note string,
	sessionId string,
) (string, error) {
	config := settings.ModelPack.Namer

	var sysPrompt string
	var tools []openai.Tool
	var toolChoice *openai.ToolChoice

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		sysPrompt = prompts.SysNoteNameXml
	} else {
		sysPrompt = prompts.SysNoteName
		tools = []openai.Tool{
			{
				Type:     "function",
				Function: &prompts.NoteNameFn,
			},
		}
		choice := openai.ToolChoice{
			Type: "function",
			Function: openai.ToolFunction{
				Name: prompts.NoteNameFn.Name,
			},
		}
		toolChoice = &choice
	}

	prompt := prompts.GetNoteNamePrompt(sysPrompt, note)

	messages := []types.ExtendedChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: prompt,
				},
			},
		},
	}

	modelRes, err := ModelRequest(ctx, ModelRequestParams{
		Clients:     clients,
		Auth:        auth,
		Plan:        plan,
		ModelConfig: &config,
		Purpose:     "Note name",
		Messages:    messages,
		Tools:       tools,
		ToolChoice:  toolChoice,
		SessionId:   sessionId,
	})

	if err != nil {
		fmt.Printf("Error during note name model call: %v\n", err)
		return "", err
	}

	var name string
	content := modelRes.Content

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		name = utils.GetXMLContent(content, "name")
		if name == "" {
			return "", fmt.Errorf("No name tag found in XML response")
		}
	} else {
		if content == "" {
			fmt.Println("no nameNote function call found in response")
			return "", fmt.Errorf("No nameNote function call found in response. The model failed to generate a valid response.")
		}

		var nameRes prompts.NoteNameRes
		err = json.Unmarshal([]byte(content), &nameRes)
		if err != nil {
			fmt.Printf("Error unmarshalling note name response: %v\n", err)
			return "", err
		}
		name = nameRes.Name
	}

	return name, nil
}
