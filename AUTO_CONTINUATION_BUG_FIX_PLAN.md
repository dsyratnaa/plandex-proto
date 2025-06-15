# Auto-Continuation Bug Fix Plan

## Issue Summary

Plandex is experiencing an auto-continuation bug where simple user prompts trigger multiple LLM responses instead of a single response. The system gets stuck in implementation mode and automatically continues generating responses even for basic questions like "What programming language is this repository primarily written in?"

## Root Cause Analysis

### Primary Issue: Persistent Implementation State
- Plans remain in `TellStage: implementation` across user sessions
- Conversation history carries over implementation context indefinitely
- Simple questions are treated as part of ongoing implementation tasks
- Auto-continuation logic triggers on EOF errors during implementation stage

### Evidence from Logs
```
message.Flags.CurrentStage.TellStage: implementation
len(convo) 28 | num 29  // 29th message in conversation
implementation stage - smart context enabled for current subtask
```

### Behavior Pattern
1. User asks simple question: "What programming language is this?"
2. System loads 28+ previous messages from implementation context
3. LLM provides correct answer, then continues with implementation tasks
4. Stream encounters EOF error
5. Auto-continuation logic triggers because `TellStage == implementation`
6. Process repeats 2-3 times until hitting retry limits

## Relevant Files for Context

### Core Auto-Continuation Logic
- `app/server/model/plan/tell_stream_finish.go` - Main auto-continuation decision logic
- `app/server/model/plan/tell_stream_status.go` - `willContinuePlan()` function
- `app/server/model/plan/tell_stage.go` - Stage resolution and planning phase logic
- `app/server/model/plan/tell_exec.go` - Plan execution and iteration management

### Stream Processing & Error Handling
- `app/server/model/plan/tell_stream_error.go` - Retry logic and error handling
- `app/server/model/plan/tell_stream_main.go` - Main stream processing loop
- `app/server/model/plan/tell_stream_processor.go` - Content processing and buffering
- `app/server/model/client_stream.go` - LLM stream management

### State Management
- `app/server/model/plan/state.go` - Active plan state management
- `app/server/model/plan/activate.go` - Plan activation logic
- `app/server/model/plan/tell_load.go` - Conversation loading and context setup
- `app/server/model/plan/tell_context.go` - Context formatting for different stages

### Configuration & Constants
- `app/server/model/client.go` - Retry limits and timeout constants
- `shared/req_res.go` - Request structures including `AutoContinue` flag
- `shared/plan_types.go` - Plan stage and phase definitions

### Database & Persistence
- `app/server/db/plan_helpers.go` - Plan persistence
- `app/server/db/convo_helpers.go` - Conversation message storage
- `app/server/handlers/plans_exec.go` - HTTP handlers for plan execution

## Fix Implementation Plan

### Step 1: Create Fix Branch
```bash
git checkout -b fix/auto-continuation-bug
git push -u origin fix/auto-continuation-bug
```

### Step 2: Implement Stage Reset Logic

#### 2.1 Modify `tell_stage.go` - Add Simple Question Detection
**File**: `app/server/model/plan/tell_stage.go`

Add function to detect simple factual questions:
```go
func isSimpleFactualQuestion(prompt string) bool {
    prompt = strings.ToLower(strings.TrimSpace(prompt))
    
    // Simple question patterns
    simplePatterns := []string{
        "what programming language",
        "what language is",
        "how many",
        "what is the",
        "what database",
        "what framework",
        "which version",
    }
    
    for _, pattern := range simplePatterns {
        if strings.Contains(prompt, pattern) {
            return true
        }
    }
    
    // Single sentence questions
    sentences := strings.Split(prompt, ".")
    if len(sentences) <= 2 && strings.Contains(prompt, "?") {
        return true
    }
    
    return false
}
```

#### 2.2 Modify Stage Resolution Logic
In `resolveCurrentStage()` function, add logic to reset to planning stage for simple questions:

```go
// After line ~50 in resolveCurrentStage()
if isUserPrompt && isSimpleFactualQuestion(state.userPrompt) {
    log.Println("[resolveCurrentStage] Simple factual question detected - resetting to planning stage")
    state.currentStage.TellStage = shared.TellStagePlanning
    state.currentStage.PlanningPhase = shared.PlanningPhaseResponse
    return
}
```

### Step 3: Improve Auto-Continuation Decision Logic

#### 3.1 Modify `tell_stream_status.go` - Enhanced `willContinuePlan()`
**File**: `app/server/model/plan/tell_stream_status.go`

Add parameters to track user intent:
```go
type willContinuePlanParams struct {
    hasNewSubtasks      bool
    removedSubtasks     bool
    allSubtasksFinished bool
    activatePaths       map[string]bool
    hasExplicitPaths    bool
    isSimpleQuestion    bool  // NEW
    userPrompt          string // NEW
}
```

Enhance the `willContinuePlan()` logic:
```go
func (state *activeTellStreamState) willContinuePlan(params willContinuePlanParams) bool {
    // NEW: Don't continue for simple questions
    if params.isSimpleQuestion {
        log.Println("[willContinuePlan] Simple question detected - stopping auto-continuation")
        return false
    }
    
    // NEW: Don't continue if user prompt is very short and no explicit tasks
    if len(strings.TrimSpace(params.userPrompt)) < 20 && !params.hasNewSubtasks {
        log.Println("[willContinuePlan] Short prompt with no new tasks - stopping")
        return false
    }
    
    // ... existing logic continues
}
```

### Step 4: Add EOF Error Classification

#### 4.1 Modify `tell_stream_error.go` - Better Error Handling
**File**: `app/server/model/plan/tell_stream_error.go`

Add logic to distinguish between legitimate completion and network errors:
```go
func (state *activeTellStreamState) shouldRetryOnEOF(streamErr error) bool {
    // If it's a simple question and we got some content, don't retry
    if isSimpleFactualQuestion(state.userPrompt) && 
       len(strings.TrimSpace(state.chunkProcessor.accumulatedContent)) > 50 {
        log.Println("EOF on simple question with content - treating as completion")
        return false
    }
    
    // If we're in implementation but no new tasks were created, don't retry
    if state.currentStage.TellStage == shared.TellStageImplementation &&
       len(state.chunkProcessor.replyOperations) == 0 {
        log.Println("EOF in implementation with no operations - treating as completion")
        return false
    }
    
    return true // Default to retry for other cases
}
```

### Step 5: Add Configuration Controls

#### 5.1 Add Auto-Continue Safeguards
**File**: `app/server/model/plan/tell_stream_finish.go`

Add additional checks before auto-continuation:
```go
// Before line 184 in tell_stream_finish.go
willContinue := state.willContinuePlan(willContinuePlanParams{
    hasNewSubtasks:      hasNewSubtasks,
    allSubtasksFinished: allSubtasksFinished,
    activatePaths:       autoLoadContextResult.activatePaths,
    removedSubtasks:     len(removedSubtasks) > 0,
    hasExplicitPaths:    autoLoadContextResult.hasExplicitPaths,
    isSimpleQuestion:    isSimpleFactualQuestion(state.userPrompt), // NEW
    userPrompt:          state.userPrompt, // NEW
})

// Add additional safeguards
if willContinue {
    // Don't continue if we've already provided a reasonable response
    contentLength := len(strings.TrimSpace(active.CurrentReplyContent))
    if contentLength > 100 && isSimpleFactualQuestion(state.userPrompt) {
        log.Printf("[AutoContinue] Simple question answered (%d chars) - stopping", contentLength)
        willContinue = false
    }
    
    // Don't continue if no explicit implementation request
    if state.currentStage.TellStage == shared.TellStageImplementation && 
       !strings.Contains(strings.ToLower(state.userPrompt), "implement") &&
       !strings.Contains(strings.ToLower(state.userPrompt), "create") &&
       !strings.Contains(strings.ToLower(state.userPrompt), "build") {
        log.Println("[AutoContinue] No explicit implementation request - stopping")
        willContinue = false
    }
}
```

### Step 6: Testing Strategy

#### 6.1 Create Test Cases
Create `test_auto_continuation_fix.go` with test scenarios:
- Simple factual questions
- Short prompts
- Implementation requests
- Mixed conversation history

#### 6.2 Manual Testing Protocol
1. Test simple questions: "What programming language is this?"
2. Test with existing conversation history
3. Test legitimate implementation requests
4. Test edge cases and error conditions

### Step 7: Monitoring & Logging

#### 7.1 Enhanced Logging
Add detailed logging for auto-continuation decisions:
```go
log.Printf("[AutoContinue] Decision: willContinue=%v, stage=%v, isSimple=%v, contentLen=%d, iteration=%d", 
    willContinue, state.currentStage.TellStage, isSimpleQuestion, contentLength, state.iteration)
```

### Step 8: Deployment & Rollback Plan

#### 8.1 Gradual Rollout
1. Deploy to development environment
2. Test with various conversation scenarios
3. Monitor auto-continuation rates
4. Deploy to production with feature flag

#### 8.2 Rollback Strategy
- Keep original logic as fallback
- Add configuration flag to disable new logic if needed
- Monitor error rates and user feedback

## Success Criteria

1. Simple factual questions receive single responses
2. Auto-continuation only triggers for legitimate implementation tasks
3. No regression in normal planning/implementation workflows
4. Reduced EOF-related retry loops
5. Improved user experience for basic queries

## Risk Assessment

- **Low Risk**: Stage detection logic changes
- **Medium Risk**: Auto-continuation logic modifications
- **High Risk**: Changes to core stream processing

## Timeline

- **Day 1**: Implement stage reset logic and simple question detection
- **Day 2**: Enhance auto-continuation decision logic
- **Day 3**: Add EOF error classification and safeguards
- **Day 4**: Testing and refinement
- **Day 5**: Documentation and deployment preparation
