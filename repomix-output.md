This file is a merged representation of a subset of the codebase, containing specifically included files, combined into a single document by Repomix.

# File Summary

## Purpose
This file contains a packed representation of the entire repository's contents.
It is designed to be easily consumable by AI systems for analysis, code review,
or other automated processes.

## File Format
The content is organized as follows:
1. This summary section
2. Repository information
3. Directory structure
4. Repository files (if enabled)
4. Multiple file entries, each consisting of:
  a. A header with the file path (## File: path/to/file)
  b. The full contents of the file in a code block

## Usage Guidelines
- This file should be treated as read-only. Any changes should be made to the
  original repository files, not this packed version.
- When processing this file, use the file path to distinguish
  between different files in the repository.
- Be aware that this file may contain sensitive information. Handle it with
  the same level of security as you would the original repository.

## Notes
- Some files may have been excluded based on .gitignore rules and Repomix's configuration
- Binary files are not included in this packed representation. Please refer to the Repository Structure section for a complete list of file paths, including binary files
- Only files matching these patterns are included: app/server/main.go, app/server/internal/tracing/tracing.go, app/server/model/client.go, app/server/model/plan/tell_exec.go, app/server/model/plan/activate.go, app/server/model/plan/build_exec.go, app/server/model/plan/tell_stream_main.go, app/server/model/plan/tell_load.go, app/server/model/plan/build_load.go, app/server/model/plan/build_structured_edits.go, app/server/model/plan/build_validate_and_fix.go, app/server/model/plan/build_whole_file.go, app/server/model/plan/commit_msg.go, app/server/model/plan/tell_summary.go, app/server/model/plan/exec_status.go
- Files matching patterns in .gitignore are excluded
- Files matching default ignore patterns are excluded
- Files are sorted by Git change count (files with more changes are at the bottom)

## Additional Info

# Directory Structure
```
app/
  server/
    internal/
      tracing/
        tracing.go
    model/
      plan/
        activate.go
        build_exec.go
        build_load.go
        build_structured_edits.go
        build_validate_and_fix.go
        build_whole_file.go
        commit_msg.go
        exec_status.go
        tell_exec.go
        tell_load.go
        tell_stream_main.go
        tell_summary.go
      client.go
    main.go
```

# Files

## File: app/server/model/plan/activate.go
```go
package plan

import (
	"fmt"
	"log"
	"plandex-server/db"
	"plandex-server/host"
	"plandex-server/model"
	"plandex-server/types"
	"time"

	shared "plandex-shared"
)

func activatePlan(
	clients map[string]model.ClientInfo,
	plan *db.Plan,
	branch string,
	auth *types.ServerAuth,
	prompt string,
	buildOnly,
	autoContext bool,
	sessionId string,
) (*types.ActivePlan, error) {
	log.Printf("Activate plan: plan ID %s on branch %s\n", plan.Id, branch)

	// Just in case this request was made immediately after another stream finished, wait a little to allow for cleanup
	log.Println("Waiting 100ms before checking for active plan")
	time.Sleep(100 * time.Millisecond)
	log.Println("Done waiting, checking for active plan")

	active := GetActivePlan(plan.Id, branch)
	if active != nil {
		log.Printf("Tell: Active plan found for plan ID %s on branch %s\n", plan.Id, branch) // Log if an active plan is found
		return nil, fmt.Errorf("plan %s branch %s already has an active stream on this host", plan.Id, branch)
	}

	modelStream, err := db.GetActiveModelStream(plan.Id, branch)
	if err != nil {
		log.Printf("Error getting active model stream: %v\n", err)
		return nil, fmt.Errorf("error getting active model stream: %v", err)
	}

	if modelStream != nil {
		log.Printf("Tell: Active model stream found for plan ID %s on branch %s on host %s\n", plan.Id, branch, modelStream.InternalIp) // Log if an active model stream is found
		return nil, fmt.Errorf("plan %s branch %s already has an active stream on host %s", plan.Id, branch, modelStream.InternalIp)
	}

	active = CreateActivePlan(
		auth.OrgId,
		auth.User.Id,
		plan.Id,
		branch,
		prompt,
		buildOnly,
		autoContext,
		sessionId,
	)

	modelStream = &db.ModelStream{
		OrgId:      auth.OrgId,
		PlanId:     plan.Id,
		InternalIp: host.Ip,
		Branch:     branch,
	}
	err = db.StoreModelStream(modelStream, active.Ctx, active.CancelFn)
	if err != nil {
		log.Printf("Tell: Error storing model stream for plan ID %s on branch %s: %v\n", plan.Id, branch, err) // Log error storing model stream
		log.Printf("Error storing model stream: %v\n", err)
		log.Printf("Tell: Error storing model stream: %v\n", err) // Log error storing model stream

		active.StreamDoneCh <- &shared.ApiError{Msg: fmt.Sprintf("Error storing model stream: %v", err)}

		return nil, fmt.Errorf("error storing model stream: %v", err)
	}

	active.ModelStreamId = modelStream.Id

	log.Printf("Tell: Model stream stored with ID %s for plan ID %s on branch %s\n", modelStream.Id, plan.Id, branch) // Log successful storage of model stream
	log.Println("Model stream id:", modelStream.Id)

	return active, nil
}
```

## File: app/server/model/plan/build_exec.go
```go
package plan

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"plandex-server/db"
	"plandex-server/hooks"
	"plandex-server/model"
	"plandex-server/notify"
	"plandex-server/types"
	"runtime/debug"
	"time"

	shared "plandex-shared"
)

func Build(
	clients map[string]model.ClientInfo,
	plan *db.Plan,
	branch string,
	auth *types.ServerAuth,
	sessionId string,
) (int, error) {
	log.Printf("Build: Called with plan ID %s on branch %s\n", plan.Id, branch)
	log.Println("Build: Starting Build operation")

	state := activeBuildStreamState{
		clients:       clients,
		auth:          auth,
		currentOrgId:  auth.OrgId,
		currentUserId: auth.User.Id,
		plan:          plan,
		branch:        branch,
	}

	streamDone := func() {
		active := GetActivePlan(plan.Id, branch)
		if active != nil {
			active.StreamDoneCh <- nil
		}
	}

	onErr := func(err error) (int, error) {
		log.Printf("Build error: %v\n", err)
		streamDone()
		return 0, err
	}

	pendingBuildsByPath, err := state.loadPendingBuilds(sessionId)
	if err != nil {
		return onErr(err)
	}

	if len(pendingBuildsByPath) == 0 {
		log.Println("No pending builds")
		streamDone()
		return 0, nil
	}

	err = db.SetPlanStatus(plan.Id, branch, shared.PlanStatusBuilding, "")

	if err != nil {
		log.Printf("Error setting plan status to building: %v\n", err)
		return onErr(fmt.Errorf("error setting plan status to building: %v", err))
	}

	log.Printf("Starting %d builds\n", len(pendingBuildsByPath))

	for _, pendingBuilds := range pendingBuildsByPath {
		go state.queueBuilds(pendingBuilds)
	}

	return len(pendingBuildsByPath), nil
}

func (state *activeBuildStreamState) queueBuild(activeBuild *types.ActiveBuild) {
	planId := state.plan.Id
	branch := state.branch

	filePath := activeBuild.Path

	// log.Printf("Queue:")
	// spew.Dump(activePlan.BuildQueuesByPath[filePath])

	var isBuilding bool

	UpdateActivePlan(planId, branch, func(active *types.ActivePlan) {
		active.BuildQueuesByPath[filePath] = append(active.BuildQueuesByPath[filePath], activeBuild)
		isBuilding = active.IsBuildingByPath[filePath]
	})
	log.Printf("Queued build for file %s\n", filePath)

	if isBuilding {
		log.Printf("Already building file %s\n", filePath)
		return
	} else {
		log.Printf("Not building file %s\n", filePath)

		active := GetActivePlan(planId, branch)
		if active == nil {
			log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
			return
		}

		UpdateActivePlan(planId, branch, func(active *types.ActivePlan) {
			active.IsBuildingByPath[filePath] = true
		})

		go state.execPlanBuild(activeBuild)
	}
}

func (state *activeBuildStreamState) queueBuilds(activeBuilds []*types.ActiveBuild) {
	log.Printf("Queueing %d builds\n", len(activeBuilds))

	for _, activeBuild := range activeBuilds {
		state.queueBuild(activeBuild)
	}
}

func (buildState *activeBuildStreamState) execPlanBuild(activeBuild *types.ActiveBuild) {
	if activeBuild == nil {
		log.Println("No active build")
		return
	}

	log.Printf("execPlanBuild - %s\n", activeBuild.Path)
	// log.Println(spew.Sdump(activeBuild))

	planId := buildState.plan.Id
	branch := buildState.branch

	activePlan := GetActivePlan(planId, branch)
	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("execPlanBuild: Panic: %v\n%s\n", r, string(debug.Stack()))

			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("execPlanBuild: Panic: %v\n%s", r, string(debug.Stack())))

			activePlan.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Panic in execPlanBuild",
			}
		}
	}()

	filePath := activeBuild.Path

	if !activePlan.IsBuildingByPath[filePath] {
		UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
			ap.IsBuildingByPath[filePath] = true
		})
	}

	fileState := &activeBuildStreamFileState{
		activeBuildStreamState: buildState,
		filePath:               filePath,
		activeBuild:            activeBuild,
		builderRun: hooks.DidFinishBuilderRunParams{
			StartedAt: time.Now(),
			PlanId:    activePlan.Id,
			FilePath:  filePath,
			FileExt:   filepath.Ext(filePath),
		},
	}

	log.Printf("execPlanBuild - %s - calling fileState.loadBuildFile()\n", filePath)
	err := fileState.loadBuildFile(activeBuild)
	if err != nil {
		log.Printf("Error loading build file: %v\n", err)
		fileState.onBuildFileError(fmt.Errorf("error loading build file: %v", err))
		return
	}

	fileState.resolvePreBuildState()

	// unless it's a file operation, stream initial status to client
	if !activeBuild.IsFileOperation() && !fileState.isNewFile {
		log.Printf("execPlanBuild - %s - streaming initial build info\n", filePath)
		// spew.Dump(activeBuild)
		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Finished:  false,
		}
		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})
	} else if activeBuild.IsFileOperation() {
		log.Printf("execPlanBuild - %s - file operation - won't stream initial build info\n", filePath)
	} else if fileState.isNewFile {
		log.Printf("execPlanBuild - %s - new file - won't stream initial build info\n", filePath)
	}

	log.Printf("execPlanBuild - %s - calling fileState.buildFile()\n", filePath)
	fileState.buildFile()
}

func (fileState *activeBuildStreamFileState) buildFile() {
	filePath := fileState.filePath
	activeBuild := fileState.activeBuild
	planId := fileState.plan.Id
	branch := fileState.branch
	currentOrgId := fileState.currentOrgId
	build := fileState.build

	activePlan := GetActivePlan(planId, branch)

	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		return
	}

	log.Printf("Building file %s\n", filePath)
	log.Printf("%d files in context\n", len(activePlan.ContextsByPath))
	// log.Println("activePlan.ContextsByPath files:")
	// for k := range activePlan.ContextsByPath {
	// 	log.Println(k)
	// }

	if activeBuild.IsMoveOp {
		log.Printf("File %s is a move operation. Moving to %s\n", filePath, activeBuild.MoveDestination)

		// For move operations, we split it into two separate builds:
		// 1. A removal build for the source file
		// 2. A creation build for the destination file with the current content
		// This is simpler than handling moves in a single build since our build system
		// is designed around operating on one path at a time
		fileState.activeBuildStreamState.queueBuilds([]*types.ActiveBuild{
			{
				ReplyId:    activeBuild.ReplyId,
				Path:       activeBuild.Path,
				IsRemoveOp: true,
			},
			{
				ReplyId:           activeBuild.ReplyId,
				Path:              activeBuild.MoveDestination,
				FileContent:       fileState.preBuildState,
				FileContentTokens: 0,
			},
		})

		// Mark this move operation as successful since we've queued the actual work
		activeBuild.Success = true

		UpdateActivePlan(planId, branch, func(active *types.ActivePlan) {
			active.IsBuildingByPath[filePath] = false
			active.BuiltFiles[filePath] = true
		})

		// Process the next build in queue (which will be our removal build)
		// We need to explicitly advance the queue for the source path since this
		// current build is holding the 'building' state open
		// The create build for the destination will be handled automatically by the queue logic
		fileState.buildNextInQueue()
		return
	}

	if activeBuild.IsRemoveOp {
		log.Printf("File %s is a remove operation. Removing file.\n", filePath)

		log.Printf("streaming remove build info for file %s\n", filePath)
		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Removed:   true,
			Finished:  true,
		}

		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})

		planRes := &db.PlanFileResult{
			OrgId:          currentOrgId,
			PlanId:         planId,
			PlanBuildId:    build.Id,
			ConvoMessageId: build.ConvoMessageId,
			Path:           filePath,
			Content:        "",
			RemovedFile:    true,
		}
		fileState.onFinishBuildFile(planRes)
		return
	}

	if activeBuild.IsResetOp {
		log.Printf("File %s is a reset operation. Resetting file.\n", filePath)

		err := db.ExecRepoOperation(db.ExecRepoOperationParams{
			OrgId:       currentOrgId,
			UserId:      fileState.currentUserId,
			PlanId:      planId,
			Branch:      branch,
			PlanBuildId: build.Id,
			Scope:       db.LockScopeWrite,
			Reason:      "reset file op",
			Ctx:         activePlan.Ctx,
			CancelFn:    activePlan.CancelFn,
		}, func(repo *db.GitRepo) error {
			now := time.Now()
			return db.RejectPlanFile(currentOrgId, planId, filePath, now)
		})

		if err != nil {
			log.Printf("Error rejecting plan file: %v\n", err)
			fileState.onBuildFileError(fmt.Errorf("error rejecting plan file: %v", err))
			return
		}

		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Finished:  true,
			Removed:   fileState.contextPart == nil,
		}

		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})

		time.Sleep(200 * time.Millisecond)

		fileState.onBuildProcessed(activeBuild)
		return
	}

	if fileState.preBuildState == "" {
		log.Printf("File %s not found in model context or current plan. Creating new file.\n", filePath)

		buildInfo := &shared.BuildInfo{
			Path:      filePath,
			NumTokens: 0,
			Finished:  true,
		}

		log.Printf("streaming new file build info for file %s\n", filePath)

		activePlan.Stream(shared.StreamMessage{
			Type:      shared.StreamMessageBuildInfo,
			BuildInfo: buildInfo,
		})

		// new file
		planRes := &db.PlanFileResult{
			OrgId:          currentOrgId,
			PlanId:         planId,
			PlanBuildId:    build.Id,
			ConvoMessageId: build.ConvoMessageId,
			Path:           filePath,
			Content:        activeBuild.FileContent,
		}

		// log.Println("build exec - new file result")
		// spew.Dump(planRes)
		fileState.onFinishBuildFile(planRes)
		return
	} else {
		currentNumTokens := shared.GetNumTokensEstimate(fileState.preBuildState)

		log.Printf("Current state num tokens: %d\n", currentNumTokens)

		activeBuild.CurrentFileTokens = currentNumTokens
		activePlan.DidEditFiles = true
	}

	// build structured edits strategy now works regardless of language/tree-sitter support
	log.Println("buildFile - building structured edits")
	fileState.buildStructuredEdits()
}

func (fileState *activeBuildStreamFileState) resolvePreBuildState() {
	filePath := fileState.filePath
	currentPlan := fileState.currentPlanState
	planId := fileState.plan.Id
	branch := fileState.branch

	activePlan := GetActivePlan(planId, branch)

	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		return
	}
	contextPart := activePlan.ContextsByPath[filePath]

	var currentState string
	currentPlanFile, fileInCurrentPlan := currentPlan.CurrentPlanFiles.Files[filePath]

	// log.Println("plan files:")
	// spew.Dump(currentPlan.CurrentPlanFiles.Files)

	if fileInCurrentPlan {
		log.Printf("File %s found in current plan.\n", filePath)
		fileState.isNewFile = false
		currentState = currentPlanFile
		// log.Println("\n\nCurrent state:\n", currentState, "\n\n")

	} else if contextPart != nil {
		log.Printf("File %s found in model context. Using context state.\n", filePath)
		fileState.isNewFile = false
		currentState = contextPart.Body
		// log.Println("\n\nCurrent state:\n", currentState, "\n\n")
	} else {
		fileState.isNewFile = true
	}

	fileState.preBuildState = currentState
	fileState.contextPart = contextPart
}
```

## File: app/server/model/plan/build_load.go
```go
package plan

import (
	"fmt"
	"log"
	"net/http"
	"plandex-server/db"
	"plandex-server/notify"
	"plandex-server/syntax"
	"plandex-server/types"

	shared "plandex-shared"
)

func (state *activeBuildStreamState) loadPendingBuilds(sessionId string) (map[string][]*types.ActiveBuild, error) {
	clients := state.clients
	plan := state.plan
	branch := state.branch
	auth := state.auth

	active, err := activatePlan(clients, plan, branch, auth, "", true, false, sessionId)

	if err != nil {
		log.Printf("Error activating plan: %v\n", err)
	}

	modelStreamId := active.ModelStreamId
	state.modelStreamId = modelStreamId

	var modelContext []*db.Context
	var pendingBuildsByPath map[string][]*types.ActiveBuild
	var settings *shared.PlanSettings

	err = db.ExecRepoOperation(db.ExecRepoOperationParams{
		OrgId:    auth.OrgId,
		UserId:   auth.User.Id,
		PlanId:   plan.Id,
		Branch:   branch,
		Scope:    db.LockScopeRead,
		Ctx:      active.Ctx,
		CancelFn: active.CancelFn,
		Reason:   "load pending builds",
	}, func(repo *db.GitRepo) error {
		errCh := make(chan error)

		go func() {
			res, err := db.GetPlanContexts(auth.OrgId, plan.Id, true, false)
			if err != nil {
				log.Printf("Error getting plan modelContext: %v\n", err)
				errCh <- fmt.Errorf("error getting plan modelContext: %v", err)
				return
			}
			modelContext = res

			errCh <- nil
		}()

		go func() {
			res, err := active.PendingBuildsByPath(auth.OrgId, auth.User.Id, nil)

			if err != nil {
				log.Printf("Error getting pending builds by path: %v\n", err)
				errCh <- fmt.Errorf("error getting pending builds by path: %v", err)
				return
			}

			pendingBuildsByPath = res

			errCh <- nil
		}()

		go func() {
			res, err := db.GetPlanSettings(plan, true)
			if err != nil {
				log.Printf("Error getting plan settings: %v\n", err)
				errCh <- fmt.Errorf("error getting plan settings: %v", err)
				return
			}

			settings = res
			errCh <- nil
		}()

		for i := 0; i < 3; i++ {
			err = <-errCh
			if err != nil {
				log.Printf("Error getting plan data: %v\n", err)
				return err
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error getting plan data: %v", err)
	}

	UpdateActivePlan(plan.Id, branch, func(ap *types.ActivePlan) {
		ap.Contexts = modelContext
		for _, context := range modelContext {
			if context.FilePath != "" {
				ap.ContextsByPath[context.FilePath] = context
			}
		}
	})

	state.modelContext = modelContext
	state.settings = settings

	return pendingBuildsByPath, nil
}

func (state *activeBuildStreamFileState) loadBuildFile(activeBuild *types.ActiveBuild) error {
	currentOrgId := state.currentOrgId
	planId := state.plan.Id
	branch := state.branch
	filePath := state.filePath

	activePlan := GetActivePlan(planId, branch)

	if activePlan == nil {
		return fmt.Errorf("active plan not found")
	}

	convoMessageId := activeBuild.ReplyId

	parser, lang, fallbackParser, fallbackLang := syntax.GetParserForPath(filePath)

	if parser != nil {
		validationRes, err := syntax.ValidateWithParsers(activePlan.Ctx, lang, parser, fallbackLang, fallbackParser, state.preBuildState)
		if err != nil {
			log.Printf(" error validating original file syntax: %v\n", err)
			return fmt.Errorf("error validating original file syntax: %v", err)
		}

		state.language = validationRes.Lang
		state.parser = validationRes.Parser

		state.builderRun.Lang = string(validationRes.Lang)

		if validationRes.TimedOut {
			state.syntaxCheckTimedOut = true
		} else if !validationRes.Valid {
			state.preBuildStateSyntaxInvalid = true
		}
	}

	build := &db.PlanBuild{
		OrgId:          currentOrgId,
		PlanId:         planId,
		ConvoMessageId: convoMessageId,
		FilePath:       filePath,
	}
	err := db.StorePlanBuild(build)

	if err != nil {
		log.Printf("Error storing plan build: %v\n", err)
		UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
			ap.IsBuildingByPath[filePath] = false
		})
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error storing plan build: %v", err))

		activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error storing plan build: " + err.Error(),
		}
		return err
	}

	var currentPlan *shared.CurrentPlanState
	var convo []*db.ConvoMessage

	log.Println("Locking repo for load build file")

	err = db.ExecRepoOperation(db.ExecRepoOperationParams{
		OrgId:       currentOrgId,
		UserId:      state.activeBuildStreamState.currentUserId,
		PlanId:      planId,
		Branch:      branch,
		PlanBuildId: build.Id,
		Scope:       db.LockScopeRead,
		Ctx:         activePlan.Ctx,
		CancelFn:    activePlan.CancelFn,
		Reason:      "load build file",
	}, func(repo *db.GitRepo) error {
		errCh := make(chan error)

		go func() {
			log.Println("loadBuildFile - Getting current plan state")
			res, err := db.GetCurrentPlanState(db.CurrentPlanStateParams{
				OrgId:  currentOrgId,
				PlanId: planId,
			})
			if err != nil {
				log.Printf("Error getting current plan state: %v\n", err)
				UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
					ap.IsBuildingByPath[filePath] = false
				})
				go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error getting current plan state: %v", err))

				activePlan.StreamDoneCh <- &shared.ApiError{
					Type:   shared.ApiErrorTypeOther,
					Status: http.StatusInternalServerError,
					Msg:    "Error getting current plan state: " + err.Error(),
				}
				errCh <- fmt.Errorf("error getting current plan state: %v", err)
				return
			}
			currentPlan = res

			log.Println("Got current plan state")
			errCh <- nil
		}()

		go func() {
			res, err := db.GetPlanConvo(currentOrgId, planId)
			if err != nil {
				log.Printf("Error getting plan convo: %v\n", err)
				errCh <- fmt.Errorf("error getting plan convo: %v", err)
				return
			}
			convo = res

			errCh <- nil
		}()

		for i := 0; i < 2; i++ {
			err = <-errCh
			if err != nil {
				log.Printf("Error getting plan data: %v\n", err)
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Error loading build file: %v\n", err)
		UpdateActivePlan(activePlan.Id, activePlan.Branch, func(ap *types.ActivePlan) {
			ap.IsBuildingByPath[filePath] = false
		})
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error loading build file: %v", err))

		activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error loading build file: " + err.Error(),
		}
		return err
	}

	state.filePath = filePath
	state.convoMessageId = convoMessageId
	state.build = build
	state.currentPlanState = currentPlan
	state.convo = convo

	return nil

}
```

## File: app/server/model/plan/build_structured_edits.go
```go
package plan

import (
	"context"
	"fmt"
	"log"
	"plandex-server/db"
	diff_pkg "plandex-server/diff"
	"plandex-server/hooks"
	"plandex-server/syntax"
	"plandex-server/utils"
	"strings"
	"time"

	shared "plandex-shared"
)

func (fileState *activeBuildStreamFileState) buildStructuredEdits() {
	filePath := fileState.filePath
	activeBuild := fileState.activeBuild
	planId := fileState.plan.Id
	branch := fileState.branch
	originalFile := fileState.preBuildState
	parser := fileState.parser

	if parser == nil {
		log.Printf("buildStructuredEdits - tree-sitter parser is nil for file %s\n", filePath)
	}

	activePlan := GetActivePlan(planId, branch)
	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		fileState.onBuildFileError(fmt.Errorf("active plan not found for plan ID %s and branch %s", planId, branch))
		return
	}

	buildCtx, cancelBuild := context.WithCancel(activePlan.Ctx)

	proposedContent := activeBuild.FileContent
	desc := activeBuild.FileDescription

	descLower := strings.ToLower(desc)
	isReplaceOrRemove := strings.Contains(descLower, "type: replace") || strings.Contains(descLower, "type: remove") || strings.Contains(descLower, "type: overwrite")

	var autoApplyRes *syntax.ApplyChangesResult
	var autoApplySyntaxErrors []string

	calledFastApply := false
	var fastApplyRes string
	fastApplyCh := make(chan string, 1)

	callFastApply := func() {
		log.Printf("buildStructuredEdits - %s - calling fast apply hook\n", filePath)
		fileState.builderRun.DidFastApply = true
		fileState.builderRun.FastApplyStartedAt = time.Now()
		calledFastApply = true

		go func() {
			res, err := hooks.ExecHook(hooks.CallFastApply, hooks.HookParams{
				FastApplyParams: &hooks.FastApplyParams{
					InitialCode: originalFile,
					EditSnippet: proposedContent,
					Language:    fileState.language,
					Ctx:         buildCtx,
				},
			})

			if err != nil {
				log.Printf("buildStructuredEdits - error executing fast apply hook: %v\n", err)
				// empty string acts as a no-op
				fastApplyCh <- ""
				return
			} else if res.FastApplyResult == nil {
				log.Printf("buildStructuredEdits - fast apply hook returned nil result\n")
				// empty string acts as a no-op
				fastApplyCh <- ""
				return
			}

			fastApplyRes = res.FastApplyResult.MergedCode
			log.Printf("buildStructuredEdits - %s - got fast apply hook result\n", filePath)
			// fmt.Printf("buildStructuredEdits - fastApplyRes:\n%s", fastApplyRes)

			fileState.builderRun.FastApplyFinishedAt = time.Now()

			fastApplyCh <- fastApplyRes
		}()
	}

	if isReplaceOrRemove {
		callFastApply()
	}

	log.Printf("buildStructuredEdits - %s - applying changes\n", filePath)
	// Apply plan logic
	log.Printf("buildStructuredEdits - %s - calling ApplyChanges\n", filePath)
	autoApplyRes = syntax.ApplyChanges(
		buildCtx,
		syntax.ApplyChangesParams{
			Original:               originalFile,
			Proposed:               proposedContent,
			Desc:                   desc,
			AddMissingStartEndRefs: true,
			Parser:                 fileState.parser,
			Language:               fileState.language,
		},
	)
	log.Printf("buildStructuredEdits - %s - got ApplyChanges result\n", filePath)
	// log.Printf("buildStructuredEdits - autoApplyRes.NewFile:\n\n%s", autoApplyRes.NewFile)
	log.Println("buildStructuredEdits - autoApplyRes.NeedsVerifyReasons:", autoApplyRes.NeedsVerifyReasons)

	autoApplySyntaxErrors = fileState.validateSyntax(buildCtx, autoApplyRes.NewFile)

	hasNeedsVerifyReasons := len(autoApplyRes.NeedsVerifyReasons) > 0

	autoApplyHasSyntaxErrors := len(autoApplySyntaxErrors) > 0
	autoApplyIsValid := !autoApplyHasSyntaxErrors && !hasNeedsVerifyReasons

	if !autoApplyIsValid && !calledFastApply {
		callFastApply()
	}

	log.Printf("buildStructuredEdits - %s - autoApplyHasSyntaxErrors: %t, hasNeedsVerifyReasons: %t, autoApplyIsValid: %t\n",
		filePath, autoApplyHasSyntaxErrors, hasNeedsVerifyReasons, autoApplyIsValid)

	updated := autoApplyRes.NewFile

	// If no problems, we trust the direct ApplyChanges result
	if autoApplyIsValid {
		log.Printf("buildStructuredEdits - %s - changes are valid, using ApplyChanges result\n", filePath)
		fileState.builderRun.AutoApplySuccess = true
	} else {
		log.Printf("buildStructuredEdits - %s - auto apply has syntax errors or NeedsVerifyReasons", filePath)
		fileState.builderRun.AutoApplyValidationReasons = make([]string, len(autoApplyRes.NeedsVerifyReasons))
		for i, reason := range autoApplyRes.NeedsVerifyReasons {
			fileState.builderRun.AutoApplyValidationReasons[i] = string(reason)
		}

		fileState.builderRun.AutoApplyValidationSyntaxErrors = autoApplySyntaxErrors

		buildRaceParams := buildRaceParams{
			updated:         updated,
			proposedContent: proposedContent,
			desc:            desc,
			reasons:         autoApplyRes.NeedsVerifyReasons,
			syntaxErrors:    autoApplySyntaxErrors,

			didCallFastApply: calledFastApply,
			fastApplyCh:      fastApplyCh,

			sessionId: activePlan.SessionId,
		}

		buildRaceResult, err := fileState.buildRace(buildCtx, cancelBuild, buildRaceParams)
		if err != nil {
			if apiErr, ok := err.(*shared.ApiError); ok {
				activePlan.StreamDoneCh <- apiErr
				return
			} else {
				log.Printf("buildStructuredEdits - %s - error building race: %v\n", filePath, err)
				fileState.onBuildFileError(fmt.Errorf("error building race: %v", err))
			}
			return
		}

		updated = buildRaceResult.content
	}

	// output diff and store build results
	buildInfo := &shared.BuildInfo{
		Path:      filePath,
		NumTokens: 0,
		Finished:  true,
	}
	log.Printf("streaming build info for finished file %s\n", filePath)
	activePlan.Stream(shared.StreamMessage{
		Type:      shared.StreamMessageBuildInfo,
		BuildInfo: buildInfo,
	})
	time.Sleep(50 * time.Millisecond)

	// strip any blank lines from beginning/end of updated file
	updated = utils.StripAddedBlankLines(originalFile, updated)

	log.Printf("buildStructuredEdits - %s - getting diff replacements\n", filePath)
	replacements, err := diff_pkg.GetDiffReplacements(originalFile, updated)
	if err != nil {
		log.Printf("buildStructuredEdits - error getting diff replacements: %v\n", err)
		fileState.onBuildFileError(fmt.Errorf("error getting diff replacements: %v", err))
		return
	}
	log.Printf("buildStructuredEdits - %s - got %d replacements\n", filePath, len(replacements))

	for _, replacement := range replacements {
		replacement.Summary = strings.TrimSpace(desc)
	}

	res := db.PlanFileResult{
		TypeVersion:    1,
		OrgId:          fileState.plan.OrgId,
		PlanId:         fileState.plan.Id,
		PlanBuildId:    fileState.build.Id,
		ConvoMessageId: fileState.convoMessageId,
		Content:        "",
		Path:           filePath,
		Replacements:   replacements,
	}

	log.Printf("buildStructuredEdits - %s - finishing build file\n", filePath)
	fileState.onFinishBuildFile(&res)
}

func (fileState *activeBuildStreamFileState) validateSyntax(buildCtx context.Context, updated string) []string {
	if fileState.parser != nil && !fileState.preBuildStateSyntaxInvalid && !fileState.syntaxCheckTimedOut {
		validationRes, err := syntax.ValidateWithParsers(buildCtx, fileState.language, fileState.parser, "", nil, updated) // fallback parser was already set as fileState.parser if needed during initial preBuildState syntax check
		if err != nil {
			log.Printf("buildStructuredEdits - error validating updated file: %v\n", err)
		} else if validationRes.TimedOut {
			log.Printf("buildStructuredEdits - syntax check timed out for updated file\n")
			fileState.syntaxCheckTimedOut = true
			return nil
		} else {
			return validationRes.Errors
		}
	}

	return nil
}
```

## File: app/server/model/plan/build_validate_and_fix.go
```go
package plan

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	diff_pkg "plandex-server/diff"
	"plandex-server/model"
	"plandex-server/model/prompts"
	"plandex-server/syntax"
	"plandex-server/types"
	"plandex-server/utils"
	shared "plandex-shared"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

const MaxValidationFixAttempts = 3

type buildValidateLoopParams struct {
	originalFile               string
	updated                    string
	proposedContent            string
	desc                       string
	syntaxErrors               []string
	reasons                    []syntax.NeedsVerifyReason
	initialPhaseOnStream       func(chunk string, buffer string) bool
	validateOnlyOnFinalAttempt bool
	maxAttempts                int
	isInitial                  bool
	sessionId                  string
}

type buildValidateLoopResult struct {
	valid   bool
	updated string
	problem string
}

func (fileState *activeBuildStreamFileState) buildValidateLoop(
	ctx context.Context,
	params buildValidateLoopParams,
) (buildValidateLoopResult, error) {
	log.Printf("Starting buildValidateLoop for file: %s", fileState.filePath)

	originalFile := params.originalFile
	updated := params.updated
	proposedContent := params.proposedContent
	desc := params.desc

	syntaxErrors := params.syntaxErrors
	numAttempts := 0

	problems := []string{}

	maxAttempts := MaxValidationFixAttempts
	if params.maxAttempts > 0 {
		maxAttempts = params.maxAttempts
	}

	for numAttempts < maxAttempts {
		currentAttempt := numAttempts + 1
		log.Printf("Starting validation attempt %d/%d", currentAttempt, MaxValidationFixAttempts)

		// check for context cancellation
		if ctx.Err() != nil {
			log.Printf("Context cancelled during attempt %d", currentAttempt)
			return buildValidateLoopResult{}, ctx.Err()
		}

		// reset retry count for each phase
		fileState.validationNumRetry = 0
		log.Printf("Reset validation retry count for attempt %d", currentAttempt)

		var onStream func(chunk string, buffer string) bool
		if numAttempts == 0 {
			onStream = params.initialPhaseOnStream
			log.Printf("Using initial phase onStream handler")
		} else {
			onStream = nil
			log.Printf("No onStream handler for attempt %d", currentAttempt)
		}

		var reasons []syntax.NeedsVerifyReason
		if numAttempts == 0 {
			reasons = params.reasons
			log.Printf("Using initial reasons for validation")
		} else {
			reasons = []syntax.NeedsVerifyReason{}
			log.Printf("Using empty reasons list for attempt %d", currentAttempt)
		}

		modelConfig := fileState.settings.ModelPack.Builder
		// if available, switch to stronger model after the first attempt failed
		if currentAttempt > 2 && modelConfig.StrongModel != nil {
			log.Printf("Switching to strong model for attempt %d", currentAttempt)
			modelConfig = *modelConfig.StrongModel
		}

		isLastAttempt := numAttempts == maxAttempts-1

		// build validate params
		validateParams := buildValidateParams{
			originalFile:    originalFile,
			updated:         updated,
			proposedContent: proposedContent,
			desc:            desc,
			onStream:        onStream,
			syntaxErrors:    syntaxErrors,
			reasons:         reasons,
			modelConfig:     &modelConfig,
			validateOnly:    isLastAttempt && params.validateOnlyOnFinalAttempt,
			phase:           currentAttempt,
			isInitial:       params.isInitial,
			sessionId:       params.sessionId,
		}

		log.Printf("Calling buildValidate for attempt %d", currentAttempt)
		res, err := fileState.buildValidate(ctx, validateParams)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.Printf("Context canceled during buildValidate")
				return buildValidateLoopResult{}, err
			}

			log.Printf("Error in buildValidate during attempt %d: %v", currentAttempt, err)
			return buildValidateLoopResult{}, fmt.Errorf("error building validate: %v", err)
		}
		updated = res.updated

		syntaxErrors = fileState.validateSyntax(ctx, updated)
		log.Printf("Found %d syntax errors after attempt %d", len(syntaxErrors), currentAttempt)

		if res.valid && len(syntaxErrors) == 0 {
			log.Printf("Validation succeeded in attempt %d", currentAttempt)
			return buildValidateLoopResult{
				valid:   res.valid,
				updated: res.updated,
			}, nil
		}

		problems = append(problems, res.problem)

		log.Printf("Validation failed in attempt %d, preparing for next attempt", currentAttempt)

		numAttempts++
	}

	log.Printf("Validation failed after %d attempts", MaxValidationFixAttempts)
	return buildValidateLoopResult{
		valid:   false,
		updated: updated,
		problem: strings.Join(problems, "\n\n"),
	}, nil
}

type buildValidateParams struct {
	originalFile    string
	updated         string
	proposedContent string
	desc            string
	syntaxErrors    []string
	reasons         []syntax.NeedsVerifyReason
	onStream        func(chunk string, buffer string) bool
	phase           int
	modelConfig     *shared.ModelRoleConfig
	validateOnly    bool
	isInitial       bool
	sessionId       string
}

type buildValidateResult struct {
	valid   bool
	updated string
	problem string
}

func (fileState *activeBuildStreamFileState) buildValidate(
	ctx context.Context,
	params buildValidateParams,
) (buildValidateResult, error) {
	log.Printf("Starting buildValidate for phase %d", params.phase)

	auth := fileState.auth
	filePath := fileState.filePath
	clients := fileState.clients
	modelConfig := params.modelConfig

	originalFile := params.originalFile
	updated := params.updated
	proposedContent := params.proposedContent
	desc := params.desc
	onStream := params.onStream
	syntaxErrors := params.syntaxErrors
	reasons := params.reasons
	// Get diff for validation
	log.Printf("Getting diffs between original and updated content")
	diff, err := diff_pkg.GetDiffs(originalFile, updated)
	if err != nil {
		log.Printf("Error getting diffs: %v", err)
		return buildValidateResult{}, fmt.Errorf("error getting diffs: %v", err)
	}

	originalWithLineNums := shared.AddLineNums(originalFile)
	proposedWithLineNums := shared.AddLineNums(proposedContent)

	maxExpectedOutputTokens := shared.GetNumTokensEstimate(originalFile)/2 + shared.GetNumTokensEstimate(proposedContent)

	// Choose prompt and tools based on preferred format

	log.Printf("Building XML validation replacements prompt")
	promptText, headNumTokens := prompts.GetValidationReplacementsXmlPrompt(prompts.ValidationPromptParams{
		Path:                 filePath,
		OriginalWithLineNums: originalWithLineNums,
		Desc:                 desc,
		ProposedWithLineNums: proposedWithLineNums,
		Diff:                 diff,
		SyntaxErrors:         syntaxErrors,
		Reasons:              reasons,
	})

	// log.Printf("Prompt to LLM: %s", promptText)

	log.Printf("Creating initial messages for phase 1")
	messages := []types.ExtendedChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: promptText,
				},
			},
		},
	}
	reqStarted := time.Now()
	fileState.builderRun.ReplacementStartedAt = reqStarted

	if params.validateOnly {
		log.Printf("Making validation-only model request")
	} else {
		log.Printf("Making validation-replacements model request")
	}
	// log.Printf("Messages: %v", messages)

	stop := []string{"<PlandexFinish/>"}
	if params.validateOnly {
		stop = []string{"<PlandexComments>", "<PlandexReplacements>"}
	}

	var willCacheNumTokens int
	isFirstPass := params.isInitial && params.phase == 1
	if !isFirstPass && modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenAI {
		willCacheNumTokens = headNumTokens
	}

	log.Printf("buildValidate - calling model.ModelRequest")
	// spew.Dump(messages)

	// Use ModelRequest for both formats
	res, err := model.ModelRequest(ctx, model.ModelRequestParams{
		Clients:        clients,
		Auth:           auth,
		Plan:           fileState.plan,
		ModelConfig:    modelConfig,
		Purpose:        "File edit",
		Messages:       messages,
		ModelStreamId:  fileState.modelStreamId,
		ConvoMessageId: fileState.convoMessageId,
		BuildId:        fileState.build.Id,
		ModelPackName:  fileState.settings.ModelPack.Name,
		Stop:           stop,
		BeforeReq: func() {
			log.Printf("Starting model request")
			fileState.builderRun.ReplacementStartedAt = time.Now()
		},
		AfterReq: func() {
			log.Printf("Finished model request")
			fileState.builderRun.ReplacementFinishedAt = time.Now()
		},
		OnStream: onStream,

		WillCacheNumTokens:    willCacheNumTokens,
		SessionId:             params.sessionId,
		EstimatedOutputTokens: maxExpectedOutputTokens,
	})

	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("Context canceled during model request")
			return buildValidateResult{}, err
		}

		log.Printf("Error calling model: %v", err)
		return fileState.validationRetryOrError(ctx, params, err)
	}

	// log.Printf("Model response:\n\n%s", res.Content)

	fileState.builderRun.GenerationIds = append(fileState.builderRun.GenerationIds, res.GenerationId)
	log.Printf("Added generation ID: %s", res.GenerationId)

	// Handle response based on format
	parseRes, err := handleXMLResponse(fileState, res.Content, originalWithLineNums, updated, params.validateOnly)

	if err != nil {
		log.Printf("Error handling response: %v", err)
		return fileState.validationRetryOrError(ctx, params, err)
	}

	log.Printf("Validation result: valid=%v", parseRes.valid)

	return parseRes, nil
}

func handleXMLResponse(
	fileState *activeBuildStreamFileState,
	content string,
	originalWithLineNums shared.LineNumberedTextType,
	updated string,
	validateOnly bool,
) (buildValidateResult, error) {
	log.Printf("Handling XML response for file: %s", fileState.filePath)

	if strings.Contains(content, "<PlandexCorrect/>") {
		log.Printf("XML response indicates changes are correct")
		fileState.builderRun.ReplacementSuccess = true
		return buildValidateResult{
			valid:   true,
			updated: updated,
		}, nil
	}

	if validateOnly {
		log.Printf("Validation-only mode, skipping replacements")
		return buildValidateResult{
			valid:   false,
			updated: updated,
		}, nil
	}

	originalFileLines := strings.Split(string(originalWithLineNums), "\n")

	incremental := originalWithLineNums

	log.Printf("Processing XML replacement blocks")

	replacementsOuter := utils.GetXMLContent(content, "PlandexReplacements")

	if replacementsOuter == "" {
		log.Printf("No replacements found in XML response")
		return buildValidateResult{
			valid:   false,
			updated: shared.RemoveLineNums(incremental),
			problem: "No replacements found in XML response",
		}, nil
	}

	replacements := utils.GetAllXMLContent(replacementsOuter, "Replacement")

	for i, replacement := range replacements {
		log.Printf("Processing replacement: %d/%d", i+1, len(replacements))

		old := utils.GetXMLContent(replacement, "Old")
		new := utils.GetXMLContent(replacement, "New")

		if old == "" {
			log.Printf("No old content found for replacement")
			return buildValidateResult{valid: false, updated: updated}, fmt.Errorf("no old content found for replacement")
		}

		old = strings.TrimSpace(old)

		// log.Printf("Old content trimmed:\n\n%s", strconv.Quote(old))

		// log.Printf("New content:\n\n%s", strconv.Quote(new))

		if !strings.HasPrefix(old, "pdx-") {
			log.Printf("Old content does not have a line number prefix for first line")
			return buildValidateResult{valid: false, updated: updated}, fmt.Errorf("old content does not have a line number prefix for first line")
		}

		oldLines := strings.Split(old, "\n")

		var lastLine string
		var lastLineNum int
		firstLine := oldLines[0]
		if len(oldLines) > 1 {
			lastLine = oldLines[len(oldLines)-1]
		}

		firstLineNum, err := shared.ExtractLineNumberWithPrefix(firstLine, "pdx-")
		if err != nil {
			log.Printf("Error extracting line number from first line: %v", err)
			return buildValidateResult{valid: false, updated: updated}, fmt.Errorf("error extracting line number from first line: %v", err)
		}

		if lastLine != "" {
			lastLineNum, err = shared.ExtractLineNumberWithPrefix(lastLine, "pdx-")
			if err != nil {
				log.Printf("Error extracting line number from last line: %v", err)
				return buildValidateResult{valid: false, updated: updated}, fmt.Errorf("error extracting line number from last line: %v", err)
			}
		}

		if lastLineNum == 0 {
			if !(firstLineNum > 0 && firstLineNum <= len(originalFileLines)) {
				log.Printf("Invalid line number for first line: %d", firstLineNum)
				return buildValidateResult{valid: false, updated: updated}, fmt.Errorf("invalid line number for first line: %d", firstLineNum)
			}
			old = originalFileLines[firstLineNum-1]
		} else {
			if !(firstLineNum > 0 && firstLineNum <= len(originalFileLines) && lastLineNum > firstLineNum && lastLineNum <= len(originalFileLines)) {
				log.Printf("Invalid line numbers for first and last lines: %d-%d", firstLineNum, lastLineNum)
				return buildValidateResult{valid: false, updated: updated}, fmt.Errorf("invalid line numbers: %d-%d", firstLineNum, lastLineNum)
			}
			old = strings.Join(originalFileLines[firstLineNum-1:lastLineNum], "\n")
		}

		// log.Printf("Applying replacement.\n\nOld:\n\n%s\n\nNew:\n\n%s", old, new)

		incremental = shared.LineNumberedTextType(strings.Replace(string(incremental), old, new, 1))

		// log.Printf("Updated content:\n\n%s", string(incremental))
	}

	var problem string

	if strings.Contains(content, "<PlandexIncorrect/>") {
		split := strings.Split(content, "<PlandexIncorrect/>")
		problem = split[0]
	} else if strings.Contains(content, "<PlandexReplacements>") {
		split := strings.Split(content, "<PlandexReplacements>")
		problem = split[0]
	}

	final := shared.RemoveLineNums(incremental)

	// log.Printf("Final content:\n\n%s", final)

	return buildValidateResult{valid: false, updated: final, problem: problem}, nil
}

func (fileState *activeBuildStreamFileState) validationRetryOrError(buildCtx context.Context, validateParams buildValidateParams, err error) (buildValidateResult, error) {
	log.Printf("Handling validation error for file: %s", fileState.filePath)
	if fileState.validationNumRetry < MaxBuildErrorRetries {
		fileState.validationNumRetry++

		log.Printf("Retrying validation (attempt %d/%d) due to error: %v",
			fileState.validationNumRetry, MaxBuildErrorRetries, err)

		activePlan := GetActivePlan(fileState.plan.Id, fileState.branch)

		if activePlan == nil {
			log.Printf("Active plan not found for plan ID %s and branch %s",
				fileState.plan.Id, fileState.branch)
			return buildValidateResult{}, fmt.Errorf("active plan not found for plan ID %s and branch %s",
				fileState.plan.Id, fileState.branch)
		}

		select {
		case <-buildCtx.Done():
			log.Printf("Context canceled during retry wait")
			return buildValidateResult{}, context.Canceled
		case <-time.After(time.Duration(fileState.validationNumRetry*fileState.validationNumRetry)*200*time.Millisecond + time.Duration(rand.Intn(500))*time.Millisecond):
			log.Printf("Retry wait completed, attempting validation again")
			break
		}

		return fileState.buildValidate(buildCtx, validateParams)
	} else {
		log.Printf("Max retries (%d) exceeded, returning error", MaxBuildErrorRetries)
		return buildValidateResult{}, err
	}
}
```

## File: app/server/model/plan/build_whole_file.go
```go
package plan

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"plandex-server/model"
	"plandex-server/model/prompts"
	"plandex-server/types"
	"plandex-server/utils"
	"time"

	shared "plandex-shared"

	"github.com/sashabaranov/go-openai"
)

func (fileState *activeBuildStreamFileState) buildWholeFileFallback(buildCtx context.Context, proposedContent string, desc string, comments string, sessionId string) (string, error) {
	auth := fileState.auth
	filePath := fileState.filePath
	clients := fileState.clients
	planId := fileState.plan.Id
	branch := fileState.branch
	originalFile := fileState.preBuildState
	config := fileState.settings.ModelPack.GetWholeFileBuilder()

	activePlan := GetActivePlan(planId, branch)

	if activePlan == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)
		fileState.onBuildFileError(fmt.Errorf("active plan not found for plan ID %s and branch %s", planId, branch))
		return "", fmt.Errorf("active plan not found for plan ID %s and branch %s", planId, branch)
	}

	originalFileWithLineNums := shared.AddLineNums(originalFile)
	proposedContentWithLineNums := shared.AddLineNums(proposedContent)

	sysPrompt, headNumTokens := prompts.GetWholeFilePrompt(filePath, originalFileWithLineNums, proposedContentWithLineNums, desc, comments)

	messages := []types.ExtendedChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: sysPrompt,
				},
			},
		},
	}

	inputTokens := model.GetMessagesTokenEstimate(messages...) + model.TokensPerRequest
	maxExpectedOutputTokens := shared.GetNumTokensEstimate(originalFile + proposedContent)

	modelConfig := config.GetRoleForInputTokens(inputTokens)
	modelConfig = modelConfig.GetRoleForOutputTokens(maxExpectedOutputTokens)

	log.Println("buildWholeFile - calling model for whole file write")

	log.Println("buildWholeFile - modelConfig.BaseModelConfig.PredictedOutputEnabled:", modelConfig.BaseModelConfig.PredictedOutputEnabled)

	var prediction string

	if modelConfig.BaseModelConfig.PredictedOutputEnabled && comments != "" {
		prediction = `
<PlandexWholeFile>
` + originalFile + `
</PlandexWholeFile>
`

	}

	// This allows proper accounting for cached input tokens even when the stream is cancelled -- OpenAI only for now
	var willCacheNumTokens int
	if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenAI {
		willCacheNumTokens = headNumTokens
	}

	log.Println("buildWholeFile - calling model.ModelRequest")
	// spew.Dump(messages)

	modelRes, err := model.ModelRequest(buildCtx, model.ModelRequestParams{
		Clients:     clients,
		Auth:        auth,
		Plan:        fileState.plan,
		ModelConfig: &config,
		Purpose:     "File edit",

		Messages:   messages,
		Prediction: prediction,

		ModelStreamId:  fileState.modelStreamId,
		ConvoMessageId: fileState.convoMessageId,
		BuildId:        fileState.build.Id,

		BeforeReq: func() {
			fileState.builderRun.BuiltWholeFile = true
			fileState.builderRun.BuildWholeFileStartedAt = time.Now()
		},

		AfterReq: func() {
			fileState.builderRun.BuildWholeFileFinishedAt = time.Now()
		},

		WillCacheNumTokens:    willCacheNumTokens,
		EstimatedOutputTokens: maxExpectedOutputTokens,

		SessionId: sessionId,
	})

	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.Printf("buildWholeFileFallback - context canceled during model request for file %s", filePath)
			return "", err
		}

		return "", fmt.Errorf("error calling model: %v", err)
	}

	fileState.builderRun.GenerationIds = append(fileState.builderRun.GenerationIds, modelRes.GenerationId)
	fileState.builderRun.BuildWholeFileFinishedAt = time.Now()

	content := modelRes.Content

	// log.Printf("buildWholeFile - %s - content:\n%s\n", filePath, content)

	wholeFile := utils.GetXMLContent(content, "PlandexWholeFile")

	if wholeFile == "" {
		log.Printf("buildWholeFile - no whole file found in response\n")
		return fileState.wholeFileRetryOrError(buildCtx, proposedContent, desc, comments, sessionId, fmt.Errorf("no whole file found in response"))
	}

	return wholeFile, nil
}

func (fileState *activeBuildStreamFileState) wholeFileRetryOrError(buildCtx context.Context, proposedContent string, desc string, comments string, sessionId string, err error) (string, error) {
	if fileState.wholeFileNumRetry < MaxBuildErrorRetries {
		fileState.wholeFileNumRetry++

		log.Printf("buildWholeFile - retrying whole file file '%s' due to error: %v\n", fileState.filePath, err)

		activePlan := GetActivePlan(fileState.plan.Id, fileState.branch)

		if activePlan == nil {
			log.Printf("buildWholeFile - active plan not found for plan ID %s and branch %s\n", fileState.plan.Id, fileState.branch)
			// fileState.onBuildFileError(fmt.Errorf("active plan not found for plan ID %s and branch %s", fileState.plan.Id, fileState.branch))
			return "", fmt.Errorf("active plan not found for plan ID %s and branch %s", fileState.plan.Id, fileState.branch)
		}

		select {
		case <-buildCtx.Done():
			log.Printf("buildWholeFile - context canceled\n")
			return "", context.Canceled
		case <-time.After(time.Duration(fileState.wholeFileNumRetry*fileState.wholeFileNumRetry)*200*time.Millisecond + time.Duration(rand.Intn(500))*time.Millisecond):
			break
		}

		return fileState.buildWholeFileFallback(buildCtx, proposedContent, desc, comments, sessionId)
	} else {
		// fileState.onBuildFileError(err)
		return "", err
	}

}
```

## File: app/server/model/plan/commit_msg.go
```go
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"plandex-server/db"
	"plandex-server/model"
	"plandex-server/model/prompts"
	"plandex-server/notify"
	"plandex-server/types"
	"plandex-server/utils"

	shared "plandex-shared"

	"github.com/sashabaranov/go-openai"
)

func (state *activeTellStreamState) genPlanDescription() (*db.ConvoMessageDescription, *shared.ApiError) {
	auth := state.auth
	plan := state.plan
	planId := plan.Id
	branch := state.branch
	settings := state.settings
	clients := state.clients
	config := settings.ModelPack.CommitMsg

	activePlan := GetActivePlan(planId, branch)
	if activePlan == nil {
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("active plan not found for plan %s and branch %s", planId, branch))

		return nil, &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    fmt.Sprintf("active plan not found for plan %s and branch %s", planId, branch),
		}
	}

	var sysPrompt string
	var tools []openai.Tool
	var toolChoice *openai.ToolChoice

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		sysPrompt = prompts.SysDescribeXml
	} else {
		sysPrompt = prompts.SysDescribe
		tools = []openai.Tool{
			{
				Type:     "function",
				Function: &prompts.DescribePlanFn,
			},
		}
		choice := openai.ToolChoice{
			Type: "function",
			Function: openai.ToolFunction{
				Name: prompts.DescribePlanFn.Name,
			},
		}
		toolChoice = &choice
	}

	messages := []types.ExtendedChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: sysPrompt,
				},
			},
		},
		{
			Role: openai.ChatMessageRoleAssistant,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: activePlan.CurrentReplyContent,
				},
			},
		},
	}

	reqParams := model.ModelRequestParams{
		Clients:        clients,
		Auth:           auth,
		Plan:           plan,
		ModelConfig:    &config,
		Purpose:        "Response summary",
		Messages:       messages,
		ModelStreamId:  state.modelStreamId,
		ConvoMessageId: state.replyId,
		SessionId:      activePlan.SessionId,
	}

	if tools != nil {
		reqParams.Tools = tools
	}
	if toolChoice != nil {
		reqParams.ToolChoice = toolChoice
	}

	modelRes, err := model.ModelRequest(activePlan.Ctx, reqParams)

	if err != nil {
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error during plan description model call: %v", err))

		return nil, &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    fmt.Sprintf("error during plan description model call: %v", err),
		}
	}

	log.Println("Plan description model call complete")

	content := modelRes.Content

	var commitMsg string

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		commitMsg = utils.GetXMLContent(content, "commitMsg")
		if commitMsg == "" {
			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("no commitMsg tag found in XML response"))

			return nil, &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "No commitMsg tag found in XML response",
			}
		}
	} else {

		if content == "" {
			fmt.Println("no describePlan function call found in response")

			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("no describePlan function call found in response"))

			return nil, &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "No describePlan function call found in response. The model failed to generate a valid response.",
			}
		}

		var desc shared.ConvoMessageDescription
		err = json.Unmarshal([]byte(content), &desc)
		if err != nil {
			fmt.Printf("Error unmarshalling plan description response: %v\n", err)

			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error unmarshalling plan description response: %v", err))

			return nil, &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    fmt.Sprintf("error unmarshalling plan description response: %v", err),
			}
		}
		commitMsg = desc.CommitMsg
	}

	return &db.ConvoMessageDescription{
		PlanId:    planId,
		CommitMsg: commitMsg,
	}, nil
}

func GenCommitMsgForPendingResults(auth *types.ServerAuth, plan *db.Plan, clients map[string]model.ClientInfo, settings *shared.PlanSettings, current *shared.CurrentPlanState, sessionId string, ctx context.Context) (string, error) {
	config := settings.ModelPack.CommitMsg

	s := ""

	num := 0
	for _, desc := range current.ConvoMessageDescriptions {
		if desc.WroteFiles && desc.DidBuild && len(desc.BuildPathsInvalidated) == 0 && desc.AppliedAt == nil {
			s += desc.CommitMsg + "\n"
			num++
		}
	}

	if num <= 1 {
		return s, nil
	}

	prompt := "Pending changes:\n\n" + s

	messages := []types.ExtendedChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: prompts.SysPendingResults,
				},
			},
		},
		{
			Role: openai.ChatMessageRoleUser,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: prompt,
				},
			},
		},
	}

	modelRes, err := model.ModelRequest(ctx, model.ModelRequestParams{
		Clients:     clients,
		Auth:        auth,
		Plan:        plan,
		ModelConfig: &config,
		Purpose:     "Commit message",
		Messages:    messages,
		SessionId:   sessionId,
	})

	if err != nil {
		fmt.Println("Generate commit message error:", err)

		return "", err
	}

	content := modelRes.Content

	if content == "" {
		return "", fmt.Errorf("no response from model")
	}

	return content, nil
}
```

## File: app/server/model/plan/exec_status.go
```go
package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"plandex-server/model"
	"plandex-server/model/prompts"
	"plandex-server/notify"
	"plandex-server/types"
	"plandex-server/utils"
	"strings"

	shared "plandex-shared"

	"github.com/sashabaranov/go-openai"
)

// controls the number steps to spent trying to finish a single subtask
// if a subtask is not finished in this number of steps, we'll give up and mark it done
// necessary to prevent infinite loops
const MaxPreviousMessages = 4

type execStatusShouldContinueResult struct {
	subtaskFinished bool
}

func (state *activeTellStreamState) execStatusShouldContinue(currentMessage string, sessionId string, ctx context.Context) (execStatusShouldContinueResult, *shared.ApiError) {
	auth := state.auth
	plan := state.plan
	settings := state.settings
	clients := state.clients
	config := settings.ModelPack.ExecStatus

	// Check subtask completion
	if state.currentSubtask != nil {
		completionMarker := fmt.Sprintf("**%s** has been completed", state.currentSubtask.Title)
		log.Printf("[ExecStatus] Checking for subtask completion marker: %q", completionMarker)
		log.Printf("[ExecStatus] Current subtask: %q", state.currentSubtask.Title)

		if strings.Contains(currentMessage, completionMarker) {
			log.Printf("[ExecStatus] ✓ Subtask completion marker found")
			return execStatusShouldContinueResult{
				subtaskFinished: true,
			}, nil

			// NOTE: tried using an LLM to verify "suspicious" subtask completions, but in practice led to too many extra LLM calls and disagreement cycles between agent roles (it's finished. no it's note! etc.)
			// now just going back to trusting the completion marker... basically it's better to err on the side of marking tasks done.

			// var potentialProblem bool

			// if len(state.chunkProcessor.replyOperations) == 0 {
			// 	log.Printf("[ExecStatus] ✗ Subtask completion marker found, but there are no operations to execute")
			// 	potentialProblem = true
			// } else {
			// wroteToPaths := map[string]bool{}
			// for _, op := range state.chunkProcessor.replyOperations {
			// 	if op.Type == shared.OperationTypeFile {
			// 		wroteToPaths[op.Path] = true
			// 	}
			// }

			// for _, path := range state.currentSubtask.UsesFiles {
			// 	if !wroteToPaths[path] {
			// 		log.Printf("[ExecStatus] ✗ Subtask completion marker found, but the operations did not write to the file %q from the 'Uses' list", path)
			// 		potentialProblem = true
			// 		break
			// 	}
			// }
			// }

			// if !potentialProblem {
			// 	log.Printf("[ExecStatus] ✓ Subtask completion marker found and no potential problem - will mark as completed")

			// 	return execStatusShouldContinueResult{
			// 		subtaskFinished: true,
			// 	}, nil
			// } else if state.currentSubtask.NumTries >= 1 {
			// 	log.Printf("[ExecStatus] ✓ Subtask completion marker found, but the operations are questionable -- marking it done anyway since it's the second try and we can't risk an infinite loop")

			// 	return execStatusShouldContinueResult{
			// 		subtaskFinished: true,
			// 	}, nil
			// } else {
			// 	log.Printf("[ExecStatus] ✗ Subtask completion marker found, but the operations are questionable -- will verify with LLM call")
			// }
		} else {
			log.Printf("[ExecStatus] ✗ No subtask completion marker found in message")
		}

		log.Println("[ExecStatus] Current subtasks state:")
		for i, task := range state.subtasks {
			log.Printf("[ExecStatus] Task %d: %q (finished=%v)", i+1, task.Title, task.IsFinished)
		}
	}

	log.Println("Checking if plan should continue based on exec status")

	fullSubtask := state.currentSubtask.Title
	fullSubtask += "\n\n" + state.currentSubtask.Description

	previousMessages := []string{}
	for _, msg := range state.convo {
		if msg.Subtask != nil && msg.Subtask.Title == state.currentSubtask.Title {
			previousMessages = append(previousMessages, msg.Message)
		}
	}

	if len(previousMessages) >= MaxPreviousMessages {
		log.Printf("[ExecStatus] ✗ Max previous messages reached - will mark as completed and move on to next subtask")
		return execStatusShouldContinueResult{
			subtaskFinished: true,
		}, nil
	}

	prompt := prompts.GetExecStatusFinishedSubtask(prompts.GetExecStatusFinishedSubtaskParams{
		UserPrompt:                 state.userPrompt,
		CurrentSubtask:             fullSubtask,
		CurrentMessage:             currentMessage,
		PreviousMessages:           previousMessages,
		PreferredModelOutputFormat: config.BaseModelConfig.PreferredModelOutputFormat,
	})

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

	modelRes, err := model.ModelRequest(ctx, model.ModelRequestParams{
		Clients:        clients,
		Auth:           auth,
		Plan:           plan,
		ModelConfig:    &config,
		Purpose:        "Task completion check",
		Messages:       messages,
		ModelStreamId:  state.modelStreamId,
		ConvoMessageId: state.replyId,
		SessionId:      sessionId,
	})

	if err != nil {
		log.Printf("[ExecStatus] Error in model call: %v", err)
		return execStatusShouldContinueResult{}, nil
	}

	content := modelRes.Content

	var reasoning string
	var subtaskFinished bool

	if config.BaseModelConfig.PreferredModelOutputFormat == shared.ModelOutputFormatXml {
		reasoning = utils.GetXMLContent(content, "reasoning")
		subtaskFinishedStr := utils.GetXMLContent(content, "subtaskFinished")
		subtaskFinished = subtaskFinishedStr == "true"

		if reasoning == "" || subtaskFinishedStr == "" {
			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("execStatusShouldContinue: missing required XML tags in response"))

			return execStatusShouldContinueResult{}, &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Missing required XML tags in response",
			}
		}
	} else {

		if content == "" {
			log.Printf("[ExecStatus] No function response found in model output")
			return execStatusShouldContinueResult{}, nil
		}

		var res types.ExecStatusResponse
		if err := json.Unmarshal([]byte(content), &res); err != nil {
			log.Printf("[ExecStatus] Failed to parse response: %v", err)
			return execStatusShouldContinueResult{}, nil
		}

		reasoning = res.Reasoning
		subtaskFinished = res.SubtaskFinished
	}

	log.Printf("[ExecStatus] Decision: subtaskFinished=%v, reasoning=%v",
		subtaskFinished, reasoning)

	return execStatusShouldContinueResult{
		subtaskFinished: subtaskFinished,
	}, nil
}
```

## File: app/server/model/plan/tell_load.go
```go
package plan

import (
	"fmt"
	"log"
	"net/http"
	"plandex-server/db"
	"plandex-server/model"
	"plandex-server/notify"
	"plandex-server/types"

	shared "plandex-shared"

	"github.com/jmoiron/sqlx"
	"github.com/sashabaranov/go-openai"
)

func (state *activeTellStreamState) loadTellPlan() error {
	clients := state.clients
	req := state.req
	auth := state.auth
	plan := state.plan
	planId := plan.Id
	branch := state.branch
	currentUserId := state.currentUserId
	currentOrgId := state.currentOrgId
	iteration := state.iteration
	missingFileResponse := state.missingFileResponse

	err := state.setActivePlan()
	if err != nil {
		return err
	}
	active := state.activePlan

	lockScope := db.LockScopeWrite
	if iteration > 0 || missingFileResponse != "" {
		lockScope = db.LockScopeRead
	}

	var modelContext []*db.Context
	var convo []*db.ConvoMessage
	var promptMsg *db.ConvoMessage
	var summaries []*db.ConvoSummary
	var subtasks []*db.Subtask
	var settings *shared.PlanSettings
	var latestSummaryTokens int
	var currentPlan *shared.CurrentPlanState

	log.Printf("[TellLoad] Tell plan - loadTellPlan - iteration: %d, missingFileResponse: %s, req.IsUserContinue: %t, lockScope: %s\n", iteration, missingFileResponse, req.IsUserContinue, lockScope)

	db.ExecRepoOperation(db.ExecRepoOperationParams{
		OrgId:    auth.OrgId,
		UserId:   auth.User.Id,
		PlanId:   planId,
		Branch:   branch,
		Scope:    lockScope,
		Ctx:      active.Ctx,
		CancelFn: active.CancelFn,
		Reason:   "load tell plan",
	}, func(repo *db.GitRepo) error {
		errCh := make(chan error)

		// get name for plan and rename if it's a draft
		go func() {
			res, err := db.GetPlanSettings(plan, true)
			if err != nil {
				log.Printf("Error getting plan settings: %v\n", err)
				errCh <- fmt.Errorf("error getting plan settings: %v", err)
				return
			}
			settings = res

			if plan.Name == "draft" {
				name, err := model.GenPlanName(
					auth,
					plan,
					settings,
					clients,
					req.Prompt,
					active.SessionId,
					active.Ctx,
				)

				if err != nil {
					log.Printf("Error generating plan name: %v\n", err)
					errCh <- fmt.Errorf("error generating plan name: %v", err)
					return
				}

				err = db.WithTx(active.Ctx, "rename plan", func(tx *sqlx.Tx) error {
					err := db.RenamePlan(planId, name, tx)

					if err != nil {
						log.Printf("Error renaming plan: %v\n", err)
						return fmt.Errorf("error renaming plan: %v", err)
					}

					err = db.IncNumNonDraftPlans(currentUserId, tx)

					if err != nil {
						log.Printf("Error incrementing num non draft plans: %v\n", err)
						return fmt.Errorf("error incrementing num non draft plans: %v", err)
					}

					return nil
				})

				if err != nil {
					log.Printf("Error renaming plan: %v\n", err)
					errCh <- fmt.Errorf("error renaming plan: %v", err)
					return
				}
			}

			errCh <- nil
		}()

		go func() {
			if iteration > 0 || missingFileResponse != "" {
				modelContext = active.Contexts
			} else {
				res, err := db.GetPlanContexts(currentOrgId, planId, true, false)
				if err != nil {
					log.Printf("Error getting plan modelContext: %v\n", err)
					errCh <- fmt.Errorf("error getting plan modelContext: %v", err)
					return
				}

				log.Printf("[TellLoad] Tell plan - loadTellPlan - modelContext: %v\n", len(modelContext))
				// for _, part := range modelContext {
				// 	log.Printf("[TellLoad] Tell plan - loadTellPlan - part: %s - %s - %s - %d tokens\n", part.ContextType, part.Name, part.FilePath, part.NumTokens)
				// }

				modelContext = res
			}

			errCh <- nil
		}()

		go func() {
			res, err := db.GetPlanConvo(currentOrgId, planId)
			if err != nil {
				log.Printf("Error getting plan convo: %v\n", err)
				errCh <- fmt.Errorf("error getting plan convo: %v", err)
				return
			}
			convo = res
			UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
				ap.MessageNum = len(convo)
			})

			promptTokens := shared.GetNumTokensEstimate(req.Prompt)
			innerErrCh := make(chan error)

			go func() {
				if iteration == 0 && missingFileResponse == "" && !req.IsUserContinue {
					num := len(convo) + 1

					log.Printf("[TellLoad] storing user message | len(convo): %d | num: %d\n", len(convo), num)

					promptMsg = &db.ConvoMessage{
						OrgId:   currentOrgId,
						PlanId:  planId,
						UserId:  currentUserId,
						Role:    openai.ChatMessageRoleUser,
						Tokens:  promptTokens,
						Num:     num,
						Message: req.Prompt,
						Flags: shared.ConvoMessageFlags{
							IsApplyDebug: req.IsApplyDebug,
							IsUserDebug:  req.IsUserDebug,
							IsChat:       req.IsChatOnly,
						},
					}

					log.Println("[TellLoad] storing user message")
					// repo.LogGitRepoState()

					_, err = db.StoreConvoMessage(repo, promptMsg, auth.User.Id, branch, true)

					if err != nil {
						log.Printf("[TellLoad] Error storing user message: %v\n", err)
						innerErrCh <- fmt.Errorf("error storing user message: %v", err)
						return
					}

					UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
						ap.MessageNum = num
					})
				}

				innerErrCh <- nil
			}()

			go func() {
				var convoMessageIds []string

				for _, convoMessage := range convo {
					convoMessageIds = append(convoMessageIds, convoMessage.Id)
				}

				log.Println("getting plan summaries")
				log.Println("convoMessageIds:", convoMessageIds)

				res, err := db.GetPlanSummaries(planId, convoMessageIds)
				if err != nil {
					log.Printf("Error getting plan summaries: %v\n", err)
					innerErrCh <- fmt.Errorf("error getting plan summaries: %v", err)
					return
				}
				summaries = res

				log.Printf("got %d plan summaries", len(summaries))

				if len(summaries) > 0 {
					latestSummaryTokens = shared.GetNumTokensEstimate(summaries[len(summaries)-1].Summary)
				}

				innerErrCh <- nil
			}()

			for i := 0; i < 2; i++ {
				err := <-innerErrCh
				if err != nil {
					errCh <- err
					return
				}
			}

			if promptMsg != nil {
				convo = append(convo, promptMsg)
			}

			errCh <- nil
		}()

		go func() {
			res, err := db.GetPlanSubtasks(auth.OrgId, planId)
			if err != nil {
				log.Printf("Error getting plan subtasks: %v\n", err)
				errCh <- fmt.Errorf("error getting plan subtasks: %v", err)
				return
			}
			subtasks = res
			errCh <- nil
		}()

		for i := 0; i < 4; i++ {
			err = <-errCh
			if err != nil {
				go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error loading plan: %v", err))

				active.StreamDoneCh <- &shared.ApiError{
					Type:   shared.ApiErrorTypeOther,
					Status: http.StatusInternalServerError,
					Msg:    fmt.Sprintf("Error loading plan: %v", err),
				}
				return err
			}
		}

		res, err := db.GetCurrentPlanState(db.CurrentPlanStateParams{
			OrgId:    currentOrgId,
			PlanId:   planId,
			Contexts: modelContext,
		})

		if err != nil {
			return fmt.Errorf("error getting current plan state: %v", err)
		}

		currentPlan = res

		return nil
	})

	if err != nil {
		log.Printf("execTellPlan: error loading tell plan: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error loading tell plan: %v", err))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error loading tell plan",
		}
		return err
	}

	state.modelContext = modelContext
	state.convo = convo
	state.promptConvoMessage = promptMsg
	state.summaries = summaries
	state.latestSummaryTokens = latestSummaryTokens
	state.settings = settings
	state.currentPlanState = currentPlan
	state.subtasks = subtasks

	for _, subtask := range state.subtasks {
		if !subtask.IsFinished {
			state.currentSubtask = subtask
			break
		}
	}

	log.Printf("[TellLoad] Subtasks: %+v", state.subtasks)
	log.Printf("[TellLoad] Current subtask: %+v", state.currentSubtask)

	state.hasContextMap = false
	state.contextMapEmpty = true
	for _, context := range state.modelContext {
		if context.ContextType == shared.ContextMapType {
			state.hasContextMap = true
			if context.NumTokens > 0 {
				state.contextMapEmpty = false
			}
			break
		}
	}

	state.hasAssistantReply = false
	for _, convoMessage := range state.convo {
		if convoMessage.Role == openai.ChatMessageRoleAssistant {
			state.hasAssistantReply = true
			break
		}
	}

	if iteration == 0 && missingFileResponse == "" {
		UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
			ap.Contexts = state.modelContext

			for _, context := range state.modelContext {
				if context.FilePath != "" {
					ap.ContextsByPath[context.FilePath] = context
				}
			}
		})
	} else if missingFileResponse == "" {
		// reset current reply content and num tokens
		UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
			ap.CurrentReplyContent = ""
			ap.NumTokens = 0
		})
	}

	// if any skipped paths have since been added to context, remove them from skipped paths
	if len(active.SkippedPaths) > 0 {
		var toUnskipPaths []string
		for contextPath := range active.ContextsByPath {
			if active.SkippedPaths[contextPath] {
				toUnskipPaths = append(toUnskipPaths, contextPath)
			}
		}
		if len(toUnskipPaths) > 0 {
			UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
				for _, path := range toUnskipPaths {
					delete(ap.SkippedPaths, path)
				}
			})
		}
	}

	return nil
}

func (state *activeTellStreamState) setActivePlan() error {
	plan := state.plan
	branch := state.branch

	active := GetActivePlan(plan.Id, branch)

	if active == nil {
		return fmt.Errorf("no active plan with id %s", plan.Id)
	}

	state.activePlan = active

	return nil
}
```

## File: app/server/model/plan/tell_stream_main.go
```go
package plan

import (
	"fmt"
	"log"
	"net/http"
	"plandex-server/model"
	"plandex-server/notify"
	"plandex-server/types"
	"runtime/debug"
	"time"

	shared "plandex-shared"

	"github.com/davecgh/go-spew/spew"
)

func (state *activeTellStreamState) listenStream(stream *model.ExtendedChatCompletionStream) {
	defer stream.Close()

	plan := state.plan
	planId := plan.Id
	branch := state.branch

	active := GetActivePlan(planId, branch)

	if active == nil {
		log.Printf("listenStream - Active plan not found for plan ID %s on branch %s\n", planId, branch)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("listenStream: Panic: %v\n%s\n", r, string(debug.Stack()))

			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("listenStream: Panic: %v\n%s", r, string(debug.Stack())))

			active.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Panic in listenStream",
			}
		}
	}()

	state.chunkProcessor = &chunkProcessor{
		replyOperations:                 []*shared.Operation{},
		chunksReceived:                  0,
		maybeRedundantOpeningTagContent: "",
		fileOpen:                        false,
		contentBuffer:                   "",
		awaitingBlockOpeningTag:         false,
		awaitingBlockClosingTag:         false,
		awaitingBackticks:               false,
	}

	// Create a timer that will trigger if no chunk is received within the specified duration
	firstTokenTimeout := firstTokenTimeout(state.totalRequestTokens)
	log.Printf("listenStream - firstTokenTimeout: %s\n", firstTokenTimeout)
	timer := time.NewTimer(firstTokenTimeout)
	defer timer.Stop()
	streamFinished := false

	modelProvider := state.modelConfig.BaseModelConfig.Provider
	modelName := state.modelConfig.BaseModelConfig.ModelName

	respCh := make(chan *types.ExtendedChatCompletionStreamResponse)
	streamErrCh := make(chan error)

	// receive chunks from the stream in a separate goroutine so that we can handle errors and timeouts — needed because stream.Recv() blocks forever
	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				streamErrCh <- err
				return
			}
			respCh <- resp
		}
	}()

mainLoop:
	for {
		select {
		case <-active.Ctx.Done():
			// The main modelContext was canceled (not the timer)
			log.Println("\nTell: stream canceled")
			state.execHookOnStop(false)
			return
		case <-timer.C:
			// Timer triggered because no new chunk was received in time
			log.Println("\nTell: stream timeout due to inactivity")
			if streamFinished {
				log.Println("Tell stream finished—timed out waiting for usage chunk")
				state.execHookOnStop(false)
				return
			} else {
				res := state.onError(onErrorParams{
					streamErr: fmt.Errorf("stream timeout due to inactivity: The AI model (%s/%s) is not responding", modelProvider, modelName),
					storeDesc: true,
					canRetry:  active.CurrentReplyContent == "", // if there was no output yet, we can retry
				})

				if res.shouldReturn {
					return
				}
				if res.shouldContinueMainLoop {
					continue mainLoop
				}
			}

		case err := <-streamErrCh:
			log.Printf("listenStream - received from streamErrCh: %v\n", err)

			if err.Error() == "context canceled" {
				log.Println("Tell: stream context canceled")
				state.execHookOnStop(false)
				return
			}

			log.Printf("Tell: error receiving stream chunk: %v\n", err)
			state.execHookOnStop(true)

			var msg string
			if active.CurrentReplyContent == "" {
				msg = fmt.Sprintf("The AI model (%s/%s) didn't respond: %v", modelProvider, modelName, err)
			} else {
				msg = fmt.Sprintf("The AI model (%s/%s) stopped responding: %v", modelProvider, modelName, err)
			}
			state.onError(onErrorParams{
				streamErr: fmt.Errorf(msg, err),
				storeDesc: true,
				canRetry:  active.CurrentReplyContent == "", // if there was no output yet, we can retry
			})
			// here we want to return no matter what -- state.onError will decide whether to retry or not
			return
		case response := <-respCh:
			// Successfully received a chunk, reset the timer
			if !timer.Stop() {
				<-timer.C
			}
			timer.Reset(model.ACTIVE_STREAM_CHUNK_TIMEOUT)

			// log.Println("tell stream main: received stream response", spew.Sdump(response))

			if response.ID != "" && state.generationId == "" {
				state.generationId = response.ID
			}

			if state.firstTokenAt.IsZero() {
				state.firstTokenAt = time.Now()
			}

			if response.Error != nil {
				log.Println("listenStream - stream finished with error", spew.Sdump(response.Error))

				modelErr := model.ClassifyModelError(response.Error.Code, response.Error.Message, nil)

				res := state.onError(onErrorParams{
					streamErr: fmt.Errorf("The AI model (%s/%s) stopped streaming with error code %d: %s", modelProvider, modelName, response.Error.Code, response.Error.Message),
					storeDesc: true,
					canRetry:  active.CurrentReplyContent == "",
					modelErr:  &modelErr,
				})
				if res.shouldReturn {
					return
				}
				if res.shouldContinueMainLoop {
					continue mainLoop
				}
			}

			if len(response.Choices) == 0 {
				if response.Usage != nil {
					state.handleUsageChunk(response.Usage)
					return
				}

				log.Println("listenStream - stream finished with no choices", spew.Sdump(response))

				// Previously we'd return an error if there were no choices, but some models do this and then keep streaming, so we'll just log it and continue, waiting for an EOF if there's a problem
				// res := state.onError(onErrorParams{
				// 	streamErr: fmt.Errorf("stream finished with no choices | The model failed to generate a valid response."),
				// 	storeDesc: true,
				// 	canRetry:  true,
				// })
				// if res.shouldReturn {
				// 	return
				// }
				// if res.shouldContinueMainLoop {
				// 	// continue instead of returning so that context cancellation is handled
				// 	continue mainLoop
				// }

				continue mainLoop
			}

			choice := response.Choices[0]

			processChunkRes := state.processChunk(choice)
			if processChunkRes.shouldReturn {
				return
			}

			handleFinished := func() handleStreamFinishedResult {
				streamFinishResult := state.handleStreamFinished()
				if streamFinishResult.shouldReturn || streamFinishResult.shouldContinueMainLoop {
					return streamFinishResult
				}

				// usage can either be included in the final chunk (openrouter) or in a separate chunk (openai)
				// if the usage chunk is included, handle it and then return out of listener
				// otherwise keep listening for the usage chunk
				if response.Usage != nil {
					state.handleUsageChunk(response.Usage)
					return handleStreamFinishedResult{
						shouldReturn: true,
					}
				}

				// Reset the timer for the usage chunk
				if !timer.Stop() {
					<-timer.C
				}
				timer.Reset(model.USAGE_CHUNK_TIMEOUT)
				streamFinished = true

				return handleStreamFinishedResult{
					shouldContinueMainLoop: true,
				}
			}

			if processChunkRes.shouldStop {
				log.Println("Model stream reached stop sequence")

				res := handleFinished()
				if res.shouldReturn {
					return
				}
				continue
			}

			if choice.FinishReason != "" {
				log.Println("Model stream finished")
				log.Println("Finish reason: ", choice.FinishReason)

				if choice.FinishReason == "error" {
					log.Println("Model stream finished with error")

					res := state.onError(onErrorParams{
						streamErr: fmt.Errorf("The AI model (%s/%s) stopped streaming with an error status", modelProvider, modelName),
						storeDesc: true,
						canRetry:  active.CurrentReplyContent == "",
					})
					if res.shouldReturn {
						return
					}
					if res.shouldContinueMainLoop {
						continue mainLoop
					}
				}

				res := handleFinished()
				if res.shouldReturn {
					return
				}
				continue
			} else if response.Usage != nil {
				state.handleUsageChunk(response.Usage)
				return
			}
			// let main loop continue
		}
	}
}

func firstTokenTimeout(tok int) time.Duration {
	const (
		base  = 90 * time.Second
		slope = 90 * time.Second
		step  = 150_000
		cap   = 15 * time.Minute
	)
	if tok <= step {
		return base
	}
	extra := time.Duration((tok-step)/step) * slope
	if extra > cap-base {
		extra = cap - base
	}
	return base + extra
}
```

## File: app/server/internal/tracing/tracing.go
```go
// File: app/server/internal/tracing/tracing.go
package tracing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.22.0" // Ensure this version matches your otel version
)

// InitTracer sets up the OTLP exporter for Logfire using HTTP/protobuf.
// It returns a function that should be deferred to shutdown the tracer provider.
func InitTracer(serviceName string) (func(context.Context) error, error) {
	ctx := context.Background()

	logfireToken := os.Getenv("LOGFIRE_TOKEN")
	if logfireToken == "" {
		return nil, errors.New("LOGFIRE_TOKEN environment variable not set")
	}
	log.Println("[TRACING] LOGFIRE_TOKEN found.")

	otelExporterOTLPEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otelExporterOTLPEndpoint == "" {
		// Default Logfire endpoint for OTLP/HTTP
		otelExporterOTLPEndpoint = "https://logfire-api.pydantic.dev" // Updated default
		log.Printf("[TRACING] OTEL_EXPORTER_OTLP_ENDPOINT not set, using default: %s", otelExporterOTLPEndpoint)
	}

	// Logfire expects OTLP/HTTP with protobuf. The otlptracehttp exporter handles this.
	// The protocol is usually inferred or part of the endpoint.
	// No explicit "OTEL_EXPORTER_OTLP_PROTOCOL" needed for otlptracehttp if endpoint is correct.

	// Configure resource attributes (service.name, etc.)
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(serviceName),
		semconv.ServiceVersionKey.String("0.1.0"), // Replace with your app's version
	)
	log.Printf("[TRACING] OpenTelemetry resource configured for service: %s", serviceName)

	// Configure OTLP HTTP exporter options
	opts := []otlptracehttp.Option{
		otlptracehttp.WithEndpoint(strings.TrimPrefix(otelExporterOTLPEndpoint, "https://")), // e.g., "logfire-api.pydantic.dev" or "logfire-api.pydantic.dev:443"
		otlptracehttp.WithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Bearer %s", logfireToken), // Standard Bearer token
			"Content-Type":  "application/x-protobuf",               // Logfire expects protobuf
		}),
	}

	// If your endpoint doesn't explicitly use https and isn't port 443,
	// and you are NOT using a secure connection (e.g. local testing without TLS),
	// you might need WithInsecure. For Logfire's cloud endpoint, this is not needed.
	if !strings.HasPrefix(otelExporterOTLPEndpoint, "https://") && !strings.Contains(otelExporterOTLPEndpoint, ":443") {
		// Check if it's a known local/dev endpoint before automatically setting insecure
		if strings.HasPrefix(otelExporterOTLPEndpoint, "localhost") || strings.HasPrefix(otelExporterOTLPEndpoint, "127.0.0.1") {
			log.Println("[TRACING] Using insecure connection for OTLP HTTP exporter (local development).")
			opts = append(opts, otlptracehttp.WithInsecure())
		}
	}

	log.Printf("[TRACING] Configuring OTLP HTTP exporter for Logfire at: %s", otelExporterOTLPEndpoint)
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP HTTP exporter: %w", err)
	}
	log.Println("[TRACING] OTLP HTTP exporter created successfully.")

	// Configure TracerProvider with batching
	// A BatchSpanProcessor is generally recommended for production.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(5*time.Second)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()), // Good for debugging, consider ParentBased(TraceIDRatio(0.1)) for production
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// Set up global propagator to W3C Trace Context (standard for inter-service propagation)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	log.Println("[TRACING] TracerProvider configured and set globally.")

	// Return the shutdown function for the TracerProvider
	return func(shutdownCtx context.Context) error {
		log.Println("[TRACING] Shutting down TracerProvider...")
		// Attempt to flush all batched spans.
		if err := tp.ForceFlush(shutdownCtx); err != nil {
			log.Printf("[TRACING] Error flushing TracerProvider: %v", err)
			// Still attempt shutdown
		}
		if err := tp.Shutdown(shutdownCtx); err != nil {
			log.Printf("[TRACING] Error shutting down TracerProvider: %v", err)
			return fmt.Errorf("failed to shutdown TracerProvider: %w", err)
		}
		log.Println("[TRACING] TracerProvider shut down successfully.")
		return nil
	}, nil
}
```

## File: app/server/model/plan/tell_exec.go
```go
package plan

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"time"

	"plandex-server/db"
	"plandex-server/hooks"
	"plandex-server/model"
	"plandex-server/notify"
	"plandex-server/types"

	shared "plandex-shared"

	"github.com/davecgh/go-spew/spew"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func Tell(clients map[string]model.ClientInfo, plan *db.Plan, branch string, auth *types.ServerAuth, req *shared.TellPlanRequest) error {
	// Start OpenTelemetry span for Tell operation
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(context.Background(), "plan.Tell",
		trace.WithAttributes(
			attribute.String("plan.id", plan.Id),
			attribute.String("plan.branch", branch),
			attribute.String("user.id", auth.User.Id),
			attribute.String("org.id", auth.OrgId),
			attribute.String("prompt", req.Prompt),
			attribute.Bool("auto_context", req.AutoContext),
			attribute.Bool("is_chat_only", req.IsChatOnly),
			attribute.String("build_mode", string(req.BuildMode)),
		),
	)
	defer span.End()

	log.Printf("Tell: Called with plan ID %s on branch %s (TraceID: %s)\n", plan.Id, branch, span.SpanContext().TraceID().String())

	_, err := activatePlan(
		clients,
		plan,
		branch,
		auth,
		req.Prompt,
		false,
		req.AutoContext,
		req.SessionId,
	)

	if err != nil {
		log.Printf("Error activating plan: %v\n", err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to activate plan")
		return err
	}

	// Pass context to execTellPlan goroutine
	go execTellPlanWithContext(ctx, execTellPlanParams{
		clients:            clients,
		plan:               plan,
		branch:             branch,
		auth:               auth,
		req:                req,
		iteration:          0,
		shouldBuildPending: !req.IsChatOnly && req.BuildMode == shared.BuildModeAuto,
	})

	log.Printf("Tell: Tell operation completed successfully for plan ID %s on branch %s (TraceID: %s)\n", plan.Id, branch, span.SpanContext().TraceID().String())
	span.SetStatus(codes.Ok, "Tell operation initiated successfully")
	return nil
}

type execTellPlanParams struct {
	clients                    map[string]model.ClientInfo
	plan                       *db.Plan
	branch                     string
	auth                       *types.ServerAuth
	req                        *shared.TellPlanRequest
	iteration                  int
	missingFileResponse        shared.RespondMissingFileChoice
	shouldBuildPending         bool
	unfinishedSubtaskReasoning string
}

// execTellPlanWithContext wraps execTellPlan with OpenTelemetry context propagation
func execTellPlanWithContext(ctx context.Context, params execTellPlanParams) {
	// Start a new span for the execTellPlan operation
	tracer := otel.Tracer("plandex-server")
	_, span := tracer.Start(ctx, "plan.execTellPlan",
		trace.WithAttributes(
			attribute.String("plan.id", params.plan.Id),
			attribute.String("plan.branch", params.branch),
			attribute.Int("iteration", params.iteration),
			attribute.Bool("should_build_pending", params.shouldBuildPending),
			attribute.String("session.id", params.req.SessionId),
			attribute.String("missing_file_response", string(params.missingFileResponse)),
			attribute.String("unfinished_subtask_reasoning", params.unfinishedSubtaskReasoning),
		),
	)
	defer span.End()

	log.Printf("[TellExec] Starting iteration %d for plan %s on branch %s (TraceID: %s)",
		params.iteration, params.plan.Id, params.branch, span.SpanContext().TraceID().String())

	// Call the original execTellPlan function
	execTellPlan(params)

	span.SetStatus(codes.Ok, "execTellPlan completed")
}

func execTellPlan(params execTellPlanParams) {
	clients := params.clients
	plan := params.plan
	branch := params.branch
	auth := params.auth
	req := params.req
	iteration := params.iteration
	missingFileResponse := params.missingFileResponse
	shouldBuildPending := params.shouldBuildPending
	unfinishedSubtaskReasoning := params.unfinishedSubtaskReasoning

	log.Printf("[TellExec] Starting iteration %d for plan %s on branch %s", iteration, plan.Id, branch)

	currentUserId := auth.User.Id
	currentOrgId := auth.OrgId

	active := GetActivePlan(plan.Id, branch)

	if active == nil {
		log.Printf("execTellPlan: Active plan not found for plan ID %s on branch %s\n", plan.Id, branch)
		return
	}

	defer func() {
		if r := recover(); r != nil {
			log.Printf("execTellPlan: Panic: %v\n%s\n", r, string(debug.Stack()))

			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("execTellPlan: Panic: %v\n%s", r, string(debug.Stack())))

			active.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Panic in execTellPlan",
			}
		}
	}()

	if missingFileResponse == "" {
		log.Println("Executing WillExecPlanHook")
		_, apiErr := hooks.ExecHook(hooks.WillExecPlan, hooks.HookParams{
			Auth: auth,
			Plan: plan,
		})

		if apiErr != nil {
			time.Sleep(100 * time.Millisecond)
			active.StreamDoneCh <- apiErr
			return
		}
	}

	planId := plan.Id
	log.Println("execTellPlan - Setting plan status to replying")
	err := db.SetPlanStatus(planId, branch, shared.PlanStatusReplying, "")
	if err != nil {
		log.Printf("Error setting plan %s status to replying: %v\n", planId, err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error setting plan %s status to replying: %v", planId, err))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error setting plan status to replying",
		}

		log.Printf("execTellPlan: execTellPlan operation completed for plan ID %s on branch %s, iteration %d\n", plan.Id, branch, iteration)
		return
	}
	log.Println("execTellPlan - Plan status set to replying")

	state := &activeTellStreamState{
		modelStreamId:       active.ModelStreamId,
		clients:             clients,
		req:                 req,
		auth:                auth,
		currentOrgId:        currentOrgId,
		currentUserId:       currentUserId,
		plan:                plan,
		branch:              branch,
		iteration:           iteration,
		missingFileResponse: missingFileResponse,
	}

	log.Println("execTellPlan - Loading tell plan")
	err = state.loadTellPlan()
	if err != nil {
		return
	}
	log.Println("execTellPlan - Tell plan loaded")

	activatePaths, activatePathsOrdered := state.resolveCurrentStage()

	var tentativeModelConfig shared.ModelRoleConfig
	var tentativeMaxTokens int
	if state.currentStage.TellStage == shared.TellStagePlanning {
		if state.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			log.Println("Tell plan - isContextStage - setting modelConfig to context loader")
			tentativeModelConfig = state.settings.ModelPack.GetArchitect()
			tentativeMaxTokens = state.settings.GetArchitectEffectiveMaxTokens()
		} else {
			plannerConfig := state.settings.ModelPack.Planner
			tentativeModelConfig = plannerConfig.ModelRoleConfig
			tentativeMaxTokens = state.settings.GetPlannerEffectiveMaxTokens()
		}
	} else if state.currentStage.TellStage == shared.TellStageImplementation {
		tentativeModelConfig = state.settings.ModelPack.GetCoder()
		tentativeMaxTokens = state.settings.GetCoderEffectiveMaxTokens()
	} else {
		log.Printf("Tell plan - execTellPlan - unknown tell stage: %s\n", state.currentStage.TellStage)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("execTellPlan: unknown tell stage: %s", state.currentStage.TellStage))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Unknown tell stage",
		}
		return
	}

	ok, tokensWithoutContext := state.dryRunCalculateTokensWithoutContext(tentativeMaxTokens, unfinishedSubtaskReasoning)
	if !ok {
		return
	}

	var planStageSharedMsgs []*types.ExtendedChatMessagePart
	var planningPhaseOnlyMsgs []*types.ExtendedChatMessagePart
	var implementationMsgs []*types.ExtendedChatMessagePart

	if state.currentStage.TellStage == shared.TellStageImplementation {
		implementationMsgs = state.formatModelContext(formatModelContextParams{
			includeMaps:         false,
			smartContextEnabled: req.SmartContext,
			includeApplyScript:  req.ExecEnabled,
		})
	} else if state.currentStage.TellStage == shared.TellStagePlanning {
		// add the shared context between planning and context phases first so it can be cached
		// this is just for the map and any manually loaded contexts - auto contexts will be added later
		planStageSharedMsgs = state.formatModelContext(formatModelContextParams{
			includeMaps:         true,
			smartContextEnabled: req.SmartContext,
			includeApplyScript:  req.ExecEnabled,
			baseOnly:            true,
			cacheControl:        true,
		})

		if state.currentStage.PlanningPhase == shared.PlanningPhaseTasks {
			if req.AutoContext {
				msg := types.ExtendedChatMessage{
					Role:    openai.ChatMessageRoleSystem,
					Content: []types.ExtendedChatMessagePart{},
				}
				for _, part := range planStageSharedMsgs {
					msg.Content = append(msg.Content, *part)
				}
				sharedMsgsTokens := model.GetMessagesTokenEstimate(msg)

				tokensRemaining := tentativeMaxTokens - (sharedMsgsTokens + tokensWithoutContext)

				if tokensRemaining < 0 {
					log.Println("tokensRemaining is negative")
					go notify.NotifyErr(notify.SeverityError, fmt.Errorf("tokensRemaining is negative"))

					active.StreamDoneCh <- &shared.ApiError{
						Type:   shared.ApiErrorTypeOther,
						Status: http.StatusInternalServerError,
						Msg:    "Max tokens exceeded before adding context",
					}
					return
				}

				planningPhaseOnlyMsgs = state.formatModelContext(formatModelContextParams{
					includeMaps:          false,
					smartContextEnabled:  req.SmartContext,
					includeApplyScript:   false, // already included in planStageSharedMsgs
					activeOnly:           true,
					activatePaths:        activatePaths,
					activatePathsOrdered: activatePathsOrdered,
					maxTokens:            int(float64(tokensRemaining) * 0.95), // leave a little extra room
				})
			} else {
				// if auto context is disabled, just dump in any remaining auto contexts, since all basic contexts have already been added in planStageSharedMsgs
				planningPhaseOnlyMsgs = state.formatModelContext(formatModelContextParams{
					includeMaps:         false,
					smartContextEnabled: req.SmartContext,
					includeApplyScript:  false, // already included in planStageSharedMsgs
					autoOnly:            true,
				})
			}
		}
	}

	getTellSysPromptParams := getTellSysPromptParams{
		planStageSharedMsgs:   planStageSharedMsgs,
		planningPhaseOnlyMsgs: planningPhaseOnlyMsgs,
		implementationMsgs:    implementationMsgs,
		contextTokenLimit:     tentativeMaxTokens,
	}

	// log.Println("getTellSysPromptParams:\n", spew.Sdump(getTellSysPromptParams))

	sysParts, err := state.getTellSysPrompt(getTellSysPromptParams)
	if err != nil {
		log.Printf("Error getting tell sys prompt: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error getting tell sys prompt: %v", err))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    err.Error(),
		}
		return
	}

	// log.Println("**sysPrompt:**\n", spew.Sdump(sysParts))

	state.messages = []types.ExtendedChatMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: sysParts,
		},
	}

	promptMessage, ok := state.resolvePromptMessage(unfinishedSubtaskReasoning)
	if !ok {
		return
	}

	// log.Println("messages:\n\n", spew.Sdump(state.messages))

	// log.Println("promptMessage:", spew.Sdump(promptMessage))

	state.tokensBeforeConvo =
		model.GetMessagesTokenEstimate(state.messages...) +
			model.GetMessagesTokenEstimate(*promptMessage) +
			state.latestSummaryTokens +
			model.TokensPerRequest

	// print out breakdown of token usage
	log.Printf("Latest summary tokens: %d\n", state.latestSummaryTokens)
	log.Printf("Total tokens before convo: %d\n", state.tokensBeforeConvo)

	var effectiveMaxTokens int
	if state.currentStage.TellStage == shared.TellStagePlanning {
		if state.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			effectiveMaxTokens = state.settings.GetArchitectEffectiveMaxTokens()
		} else {
			effectiveMaxTokens = state.settings.GetPlannerEffectiveMaxTokens()
		}
	} else if state.currentStage.TellStage == shared.TellStageImplementation {
		effectiveMaxTokens = state.settings.GetCoderEffectiveMaxTokens()
	}

	if state.tokensBeforeConvo > effectiveMaxTokens {
		// token limit already exceeded before adding conversation
		err := fmt.Errorf("token limit exceeded before adding conversation")
		log.Printf("Error: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("token limit exceeded before adding conversation"))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Token limit exceeded before adding conversation",
		}
		return
	}

	if !state.addConversationMessages() {
		return
	}

	// add the prompt message to the end of the messages slice
	if promptMessage != nil {
		state.messages = append(state.messages, *promptMessage)
	} else {
		log.Println("promptMessage is nil")
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("promptMessage is nil"))

		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Prompt message isn't set",
		}
		return
	}

	state.replyId = uuid.New().String()
	state.replyParser = types.NewReplyParser()

	if missingFileResponse != "" && !state.handleMissingFileResponse(unfinishedSubtaskReasoning) {
		return
	}

	// filter out any messages that are empty
	state.messages = model.FilterEmptyMessages(state.messages)

	log.Printf("\n\nMessages: %d\n", len(state.messages))
	// for _, message := range state.messages {
	// 	log.Printf("%s: %v\n", message.Role, message.Content)
	// }

	requestTokens := model.GetMessagesTokenEstimate(state.messages...) + model.TokensPerRequest
	state.totalRequestTokens = requestTokens

	modelConfig := tentativeModelConfig

	log.Println("Tell plan - setting modelConfig")
	log.Println("Tell plan - requestTokens:", requestTokens)
	log.Println("Tell plan - state.currentStage.TellStage:", state.currentStage.TellStage)
	log.Println("Tell plan - state.currentStage.PlanningPhase:", state.currentStage.PlanningPhase)

	if state.currentStage.TellStage == shared.TellStagePlanning {
		if state.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			log.Println("Tell plan - isContextStage - setting modelConfig to context loader")
			modelConfig = state.settings.ModelPack.GetArchitect().GetRoleForInputTokens(requestTokens)
			log.Println("Tell plan - got modelConfig for context phase")
		} else if state.currentStage.PlanningPhase == shared.PlanningPhaseTasks {
			modelConfig = state.settings.ModelPack.Planner.GetRoleForInputTokens(requestTokens)
			log.Println("Tell plan - got modelConfig for tasks phase")
		}
	} else if state.currentStage.TellStage == shared.TellStageImplementation {
		modelConfig = state.settings.ModelPack.GetCoder().GetRoleForInputTokens(requestTokens)
		log.Println("Tell plan - got modelConfig for implementation stage")
	}

	// log.Println("Tell plan - modelConfig:", spew.Sdump(modelConfig))
	state.modelConfig = &modelConfig

	// if the model doesn't support cache control, remove the cache control spec from the messages
	if !modelConfig.BaseModelConfig.SupportsCacheControl {
		for i := range state.messages {
			for j := range state.messages[i].Content {
				if state.messages[i].Content[j].CacheControl != nil {
					state.messages[i].Content[j].CacheControl = nil
				}
			}
		}
	}

	// if the model doesn't support images, remove any image parts from the messages
	if !modelConfig.BaseModelConfig.HasImageSupport {
		log.Println("Tell exec - model doesn't support images. Removing image parts from messages. File name will still be included.")

		for i := range state.messages {
			filteredContent := []types.ExtendedChatMessagePart{}
			for _, part := range state.messages[i].Content {
				if part.Type != openai.ChatMessagePartTypeImageURL {
					filteredContent = append(filteredContent, part)
				}
			}
			state.messages[i].Content = filteredContent
		}
	}

	log.Println("tell exec - will send model request with:", spew.Sdump(map[string]interface{}{
		"provider": modelConfig.BaseModelConfig.Provider,
		"model":    modelConfig.BaseModelConfig.ModelName,
		"tokens":   requestTokens,
	}))

	_, apiErr := hooks.ExecHook(hooks.WillSendModelRequest, hooks.HookParams{
		Auth: auth,
		Plan: plan,
		WillSendModelRequestParams: &hooks.WillSendModelRequestParams{
			InputTokens:  requestTokens,
			OutputTokens: modelConfig.BaseModelConfig.MaxOutputTokens - requestTokens,
			ModelName:    modelConfig.BaseModelConfig.ModelName,
			IsUserPrompt: true,
		},
	})
	if apiErr != nil {
		active.StreamDoneCh <- apiErr
		return
	}

	state.doTellRequest()

	if shouldBuildPending {
		go state.queuePendingBuilds()
	}

	UpdateActivePlan(planId, branch, func(ap *types.ActivePlan) {
		ap.CurrentStreamingReplyId = state.replyId
		ap.CurrentReplyDoneCh = make(chan bool, 1)
	})

}

func (state *activeTellStreamState) doTellRequest() {
	clients := state.clients
	modelConfig := state.modelConfig
	active := state.activePlan

	fallbackRes := modelConfig.GetFallbackForModelError(state.numErrorRetry, state.modelErr)
	modelConfig = fallbackRes.ModelRoleConfig
	stop := []string{"<PlandexFinish/>"}

	// log.Println("Stop:", stop)
	// spew.Dump(state.messages)

	// log.Println("modelConfig:", spew.Sdump(modelConfig))

	if state.noCacheSupportErr {
		log.Println("Tell exec - request failed with cache support error. Removing cache control breakpoints from messages.")
		for i := range state.messages {
			for j := range state.messages[i].Content {
				if state.messages[i].Content[j].CacheControl != nil {
					state.messages[i].Content[j].CacheControl = nil
				}
			}
		}
	}

	modelReq := types.ExtendedChatCompletionRequest{
		Model:    modelConfig.BaseModelConfig.ModelName,
		Messages: state.messages,
		Stream:   true,
		StreamOptions: &openai.StreamOptions{
			IncludeUsage: true,
		},
		Temperature: modelConfig.Temperature,
		TopP:        modelConfig.TopP,
	}

	if modelConfig.BaseModelConfig.StopDisabled {
		state.manualStop = stop
	} else {
		modelReq.Stop = stop
	}

	// update state
	state.fallbackRes = fallbackRes
	state.requestStartedAt = time.Now()
	state.originalReq = &modelReq
	state.modelConfig = modelConfig

	// output the modelReq to a json file
	// if jsonData, err := json.MarshalIndent(modelReq, "", "  "); err == nil {
	// 	timestamp := time.Now().Format("2006-01-02-150405")
	// 	filename := fmt.Sprintf("generations/model-request-%s.json", timestamp)
	// 	if err := os.WriteFile(filename, jsonData, 0644); err != nil {
	// 		log.Printf("Error writing model request to file: %v\n", err)
	// 	}
	// } else {
	// 	log.Printf("Error marshaling model request to JSON: %v\n", err)
	// }

	log.Printf("[Tell] doTellRequest retry=%d fallbackRetry=%d using model=%s",
		state.numErrorRetry, state.numFallbackRetry, state.modelConfig.BaseModelConfig.ModelName)

	// start the stream
	stream, err := model.CreateChatCompletionStream(clients, modelConfig, active.ModelStreamCtx, modelReq)
	if err != nil {
		log.Printf("Error starting reply stream: %v\n", err)
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error starting reply stream: %v", err))
		active.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Error starting reply stream: " + err.Error(),
		}
		return
	}

	// handle stream chunks
	go state.listenStream(stream)
}

func (state *activeTellStreamState) dryRunCalculateTokensWithoutContext(tentativeMaxTokens int, unfinishedSubtaskReasoning string) (bool, int) {
	clone := &activeTellStreamState{
		modelStreamId:       state.modelStreamId,
		clients:             state.clients,
		req:                 state.req,
		auth:                state.auth,
		currentOrgId:        state.currentOrgId,
		currentUserId:       state.currentUserId,
		plan:                state.plan,
		branch:              state.branch,
		iteration:           state.iteration,
		missingFileResponse: state.missingFileResponse,
		settings:            state.settings,
		currentStage:        state.currentStage,
		subtasks:            state.subtasks,
		currentSubtask:      state.currentSubtask,
		convo:               state.convo,
		summaries:           state.summaries,
		latestSummaryTokens: state.latestSummaryTokens,
		userPrompt:          state.userPrompt,
		promptMessage:       state.promptMessage,
		hasContextMap:       state.hasContextMap,
		contextMapEmpty:     state.contextMapEmpty,
		hasAssistantReply:   state.hasAssistantReply,
		modelContext:        state.modelContext,
		activePlan:          state.activePlan,
	}

	sysParts, err := clone.getTellSysPrompt(getTellSysPromptParams{
		contextTokenLimit:    tentativeMaxTokens,
		dryRunWithoutContext: true,
	})

	if err != nil {
		log.Printf("error getting tell sys prompt for dry run token calculation: %v", err)

		msg := "Error getting tell sys prompt for dry run token calculation"
		if err.Error() == AllTasksCompletedMsg {
			msg = "There's no current task to implement. Try a prompt instead of the 'continue' command."
			go notify.NotifyErr(notify.SeverityInfo, msg)
		} else {
			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("error getting tell sys prompt for dry run token calculation: %v", err))
		}

		state.activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    msg,
		}
		return false, 0
	}

	clone.messages = []types.ExtendedChatMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: sysParts,
		},
	}

	promptMessage, ok := clone.resolvePromptMessage(unfinishedSubtaskReasoning)
	if !ok {
		return false, 0
	}

	clone.tokensBeforeConvo =
		model.GetMessagesTokenEstimate(clone.messages...) +
			model.GetMessagesTokenEstimate(*promptMessage) +
			clone.latestSummaryTokens +
			model.TokensPerRequest

	var effectiveMaxTokens int
	if clone.currentStage.TellStage == shared.TellStagePlanning {
		if clone.currentStage.PlanningPhase == shared.PlanningPhaseContext {
			effectiveMaxTokens = clone.settings.GetArchitectEffectiveMaxTokens()
		} else {
			effectiveMaxTokens = clone.settings.GetPlannerEffectiveMaxTokens()
		}
	} else if clone.currentStage.TellStage == shared.TellStageImplementation {
		effectiveMaxTokens = clone.settings.GetCoderEffectiveMaxTokens()
	}

	if clone.tokensBeforeConvo > effectiveMaxTokens {
		log.Println("tokensBeforeConvo exceeds max tokens during dry run")
		go notify.NotifyErr(notify.SeverityError, fmt.Errorf("tokensBeforeConvo exceeds max tokens during dry run"))

		state.activePlan.StreamDoneCh <- &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    "Max tokens exceeded before adding conversation",
		}
		return false, 0
	}

	if !clone.addConversationMessages() {
		return false, 0
	}

	clone.messages = append(clone.messages, *promptMessage)

	return true, model.GetMessagesTokenEstimate(clone.messages...) + model.TokensPerRequest
}
```

## File: app/server/model/plan/tell_summary.go
```go
package plan

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"plandex-server/db"
	"plandex-server/model"
	"plandex-server/model/prompts"
	"plandex-server/notify"
	"plandex-server/types"
	"time"

	shared "plandex-shared"

	"github.com/davecgh/go-spew/spew"
	"github.com/sashabaranov/go-openai"
)

func (state *activeTellStreamState) addConversationMessages() bool {
	summaries := state.summaries
	tokensBeforeConvo := state.tokensBeforeConvo
	active := GetActivePlan(state.plan.Id, state.branch)

	convo := []*db.ConvoMessage{}
	for _, msg := range state.convo {
		if state.skipConvoMessages != nil && state.skipConvoMessages[msg.Id] {
			continue
		}
		convo = append(convo, msg)
	}

	if active == nil {
		log.Println("summarizeMessagesIfNeeded - Active plan not found")
		return false
	}

	conversationTokens := 0
	tokensUpToTimestamp := make(map[int64]int)
	for _, convoMessage := range convo {
		conversationTokens += convoMessage.Tokens + model.TokensPerMessage + model.TokensPerName
		timestamp := convoMessage.CreatedAt.UnixNano() / int64(time.Millisecond)
		tokensUpToTimestamp[timestamp] = conversationTokens
		// log.Printf("Timestamp: %s | Tokens: %d | Total: %d | conversationTokens\n", convoMessage.Timestamp, convoMessage.Tokens, conversationTokens)
	}

	log.Printf("Conversation tokens: %d\n", conversationTokens)
	log.Printf("Max conversation tokens: %d\n", state.settings.GetPlannerMaxConvoTokens())

	// log.Println("Tokens up to timestamp:")
	// spew.Dump(tokensUpToTimestamp)

	log.Printf("Total tokens: %d\n", tokensBeforeConvo+conversationTokens)
	log.Printf("Max tokens: %d\n", state.settings.GetPlannerEffectiveMaxTokens())

	var summary *db.ConvoSummary
	if (tokensBeforeConvo+conversationTokens) > state.settings.GetPlannerEffectiveMaxTokens() ||
		conversationTokens > state.settings.GetPlannerMaxConvoTokens() {
		log.Println("Token limit exceeded. Attempting to reduce via conversation summary.")

		// log.Printf("(tokensBeforeConvo+conversationTokens) > state.settings.GetPlannerEffectiveMaxTokens(): %v\n", (tokensBeforeConvo+conversationTokens) > state.settings.GetPlannerEffectiveMaxTokens())
		// log.Printf("conversationTokens > state.settings.GetPlannerMaxConvoTokens(): %v\n", conversationTokens > state.settings.GetPlannerMaxConvoTokens())

		log.Printf("Num summaries: %d\n", len(summaries))

		// token limit exceeded after adding conversation
		// get summary for as much as the conversation as necessary to stay under the token limit
		for _, s := range summaries {
			timestamp := s.LatestConvoMessageCreatedAt.UnixNano() / int64(time.Millisecond)

			tokens, ok := tokensUpToTimestamp[timestamp]

			log.Printf("Last message timestamp: %d | found: %v\n", timestamp, ok)
			log.Printf("Tokens up to timestamp: %d\n", tokens)

			if !ok {
				err := fmt.Errorf("conversation summary timestamp not found in conversation")
				log.Printf("Error: %v\n", err)

				log.Println("timestamp:", timestamp)

				// log.Println("Conversation summary:")
				// spew.Dump(s)

				log.Println("tokensUpToTimestamp:")
				log.Println(spew.Sdump(tokensUpToTimestamp))

				go notify.NotifyErr(notify.SeverityError, fmt.Errorf("conversation summary timestamp not found in conversation"))

				active.StreamDoneCh <- &shared.ApiError{
					Type:   shared.ApiErrorTypeOther,
					Status: http.StatusInternalServerError,
					Msg:    "Conversation summary timestamp not found in conversation",
				}
				return false
			}

			updatedConversationTokens := (conversationTokens - tokens) + s.Tokens
			savedTokens := conversationTokens - updatedConversationTokens

			log.Printf("Conversation summary tokens: %d\n", tokens)
			log.Printf("Updated conversation tokens: %d\n", updatedConversationTokens)
			log.Printf("Saved tokens: %d\n", savedTokens)

			if updatedConversationTokens <= state.settings.GetPlannerMaxConvoTokens() &&
				(tokensBeforeConvo+updatedConversationTokens) <= state.settings.GetPlannerEffectiveMaxTokens() {
				log.Printf("Summarizing up to %s | saving %d tokens\n", s.LatestConvoMessageCreatedAt.Format(time.RFC3339), savedTokens)
				summary = s
				conversationTokens = updatedConversationTokens
				break
			}
		}

		if summary == nil && tokensBeforeConvo+conversationTokens > state.settings.GetPlannerEffectiveMaxTokens() {
			err := errors.New("couldn't get under token limit with conversation summary")
			log.Printf("Error: %v\n", err)
			go notify.NotifyErr(notify.SeverityInfo, fmt.Errorf("couldn't get under token limit with conversation summary"))

			active.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Exceeded token limit",
			}
			return false
		}
	}

	var latestSummary *db.ConvoSummary
	if len(summaries) > 0 {
		latestSummary = summaries[len(summaries)-1]
	}

	if summary == nil {
		for _, convoMessage := range convo {
			// this gets added later in tell_exec.go
			if state.promptConvoMessage != nil && convoMessage.Id == state.promptConvoMessage.Id {
				continue
			}

			state.messages = append(state.messages, types.ExtendedChatMessage{
				Role: openai.ChatMessageRoleUser,
				Content: []types.ExtendedChatMessagePart{
					{
						Type: openai.ChatMessagePartTypeText,
						Text: convoMessage.Message,
					},
				},
			})

			// add the latest summary as a conversation message if this is the last message summarized, in order to reinforce the current state of the plan to the model
			if latestSummary != nil && convoMessage.Id == latestSummary.LatestConvoMessageId {
				state.messages = append(state.messages, types.ExtendedChatMessage{
					Role: openai.ChatMessageRoleAssistant,
					Content: []types.ExtendedChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: latestSummary.Summary,
						},
					},
				})
			}
		}
	} else {
		if (tokensBeforeConvo + conversationTokens) > state.settings.GetPlannerEffectiveMaxTokens() {
			go notify.NotifyErr(notify.SeverityError, fmt.Errorf("token limit still exceeded after summarizing conversation"))

			active.StreamDoneCh <- &shared.ApiError{
				Type:   shared.ApiErrorTypeOther,
				Status: http.StatusInternalServerError,
				Msg:    "Token limit still exceeded after summarizing conversation",
			}
			return false
		}
		state.summarizedToMessageId = summary.LatestConvoMessageId
		state.messages = append(state.messages, types.ExtendedChatMessage{
			Role: openai.ChatMessageRoleAssistant,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: summary.Summary,
				},
			},
		})

		// add messages after the last message in the summary
		for _, convoMessage := range convo {
			// this gets added later in tell_exec.go
			if state.promptConvoMessage != nil && convoMessage.Id == state.promptConvoMessage.Id {
				continue
			}

			if convoMessage.CreatedAt.After(summary.LatestConvoMessageCreatedAt) {
				state.messages = append(state.messages, types.ExtendedChatMessage{
					Role: openai.ChatMessageRoleUser,
					Content: []types.ExtendedChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: convoMessage.Message,
						},
					},
				})

				// add the latest summary as a conversation message if this is the last message summarized, in order to reinforce the current state of the plan to the model
				if latestSummary != nil && convoMessage.Id == latestSummary.LatestConvoMessageId {
					state.messages = append(state.messages, types.ExtendedChatMessage{
						Role: openai.ChatMessageRoleAssistant,
						Content: []types.ExtendedChatMessagePart{
							{
								Type: openai.ChatMessagePartTypeText,
								Text: latestSummary.Summary,
							},
						},
					})
				}
			}
		}
	}

	return true
}

type summarizeConvoParams struct {
	auth                  *types.ServerAuth
	plan                  *db.Plan
	branch                string
	convo                 []*db.ConvoMessage
	summaries             []*db.ConvoSummary
	userPrompt            string
	currentReply          string
	currentReplyNumTokens int
	currentOrgId          string
	modelPackName         string
}

func summarizeConvo(clients map[string]model.ClientInfo, config shared.ModelRoleConfig, params summarizeConvoParams, ctx context.Context) *shared.ApiError {
	plan := params.plan
	planId := plan.Id
	log.Printf("summarizeConvo: Called for plan ID %s on branch %s\n", planId, params.branch)
	log.Printf("summarizeConvo: Starting summarizeConvo for planId: %s\n", planId)

	branch := params.branch
	convo := params.convo
	summaries := params.summaries
	userPrompt := params.userPrompt
	currentReply := params.currentReply
	active := GetActivePlan(planId, branch)

	if active == nil {
		log.Printf("Active plan not found for plan ID %s and branch %s\n", planId, branch)

		return &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    fmt.Sprintf("active plan not found for plan ID %s and branch %s", planId, branch),
		}
	}

	log.Println("Generating plan summary for planId:", planId)

	// log.Printf("planId: %s\n", planId)
	// log.Printf("convo: ")
	// spew.Dump(convo)
	// log.Printf("summaries: ")
	// spew.Dump(summaries)
	// log.Printf("promptMessage: ")
	// spew.Dump(promptMessage)
	// log.Printf("currentOrgId: %s\n", currentOrgId)

	var summaryMessages []*types.ExtendedChatMessage
	var latestSummary *db.ConvoSummary
	var numMessagesSummarized int = 0
	var latestMessageSummarizedAt time.Time
	var latestMessageId string
	if len(summaries) > 0 {
		latestSummary = summaries[len(summaries)-1]
		numMessagesSummarized = latestSummary.NumMessages
	}

	// log.Println("Generating plan summary - latest summary:")
	// spew.Dump(latestSummary)

	// log.Println("Generating plan summary - convo:")
	// spew.Dump(convo)

	numTokens := 0

	if latestSummary == nil {
		for _, convoMessage := range convo {
			summaryMessages = append(summaryMessages, &types.ExtendedChatMessage{
				Role: openai.ChatMessageRoleUser,
				Content: []types.ExtendedChatMessagePart{
					{
						Type: openai.ChatMessagePartTypeText,
						Text: convoMessage.Message,
					},
				},
			})
			latestMessageId = convoMessage.Id
			latestMessageSummarizedAt = convoMessage.CreatedAt
			numMessagesSummarized++
			numTokens += convoMessage.Tokens + model.TokensPerMessage + model.TokensPerName
		}
	} else {
		summaryMessages = append(summaryMessages, &types.ExtendedChatMessage{
			Role: openai.ChatMessageRoleAssistant,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: latestSummary.Summary,
				},
			},
		})

		numTokens += latestSummary.Tokens + model.TokensPerMessage + model.TokensPerName

		var found bool
		for _, convoMessage := range convo {
			if convoMessage.Id == latestSummary.LatestConvoMessageId {
				found = true
				continue
			}
			if found {
				summaryMessages = append(summaryMessages, &types.ExtendedChatMessage{
					Role: openai.ChatMessageRoleUser,
					Content: []types.ExtendedChatMessagePart{
						{
							Type: openai.ChatMessagePartTypeText,
							Text: convoMessage.Message,
						},
					},
				})
				numMessagesSummarized++
				numTokens += convoMessage.Tokens + model.TokensPerMessage + model.TokensPerName
			}
		}

		latestConvoMessage := convo[len(convo)-1]
		latestMessageId = latestConvoMessage.Id
		latestMessageSummarizedAt = latestConvoMessage.CreatedAt
	}

	log.Println("generating summary - latestMessageId:", latestMessageId)
	log.Println("generating summary - latestMessageSummarizedAt:", latestMessageSummarizedAt)

	if userPrompt != "" {
		if userPrompt != prompts.UserContinuePrompt && userPrompt != prompts.AutoContinuePlanningPrompt && userPrompt != prompts.AutoContinueImplementationPrompt {
			summaryMessages = append(summaryMessages, &types.ExtendedChatMessage{
				Role: openai.ChatMessageRoleUser,
				Content: []types.ExtendedChatMessagePart{
					{
						Type: openai.ChatMessagePartTypeText,
						Text: userPrompt,
					},
				},
			})

			tokens := shared.GetNumTokensEstimate(userPrompt)
			numTokens += tokens + model.TokensPerMessage + model.TokensPerName
		}
	}

	if currentReply != "" {
		summaryMessages = append(summaryMessages, &types.ExtendedChatMessage{
			Role: openai.ChatMessageRoleAssistant,
			Content: []types.ExtendedChatMessagePart{
				{
					Type: openai.ChatMessagePartTypeText,
					Text: currentReply,
				},
			},
		})

		numTokens += params.currentReplyNumTokens + model.TokensPerMessage + model.TokensPerName
	}

	log.Printf("Calling model for plan summary. Summarizing %d messages\n", len(summaryMessages))

	// log.Println("Generating summary - summary messages:")
	// spew.Dump(summaryMessages)

	// latestSummaryCh := make(chan *db.ConvoSummary, 1)
	// active.LatestSummaryCh = latestSummaryCh

	log.Printf("summarizeConvo: Clients map before calling model.PlanSummary: %s", spew.Sdump(clients))
	log.Printf("summarizeConvo: ModelRoleConfig before calling model.PlanSummary: %s", spew.Sdump(config))

	summary, apiErr := model.PlanSummary(clients, config, model.PlanSummaryParams{
		Conversation:                summaryMessages,
		ConversationNumTokens:       numTokens,
		LatestConvoMessageId:        latestMessageId,
		LatestConvoMessageCreatedAt: latestMessageSummarizedAt,
		NumMessages:                 numMessagesSummarized,
		Auth:                        params.auth,
		Plan:                        plan,
		ModelPackName:               params.modelPackName,
		ModelStreamId:               active.ModelStreamId,
		SessionId:                   active.SessionId,
	}, ctx)

	if apiErr != nil {
		log.Printf("summarizeConvo: Error generating plan summary for plan %s: %v\n", planId, apiErr)
		return apiErr
	}

	log.Printf("summarizeConvo: Summary generated and stored for plan %s\n", planId)

	// log.Println("Generated summary:")
	// spew.Dump(summary)

	err := db.StoreSummary(summary)

	if err != nil {
		log.Printf("Error storing plan summary for plan %s: %v\n", planId, err)
		return &shared.ApiError{
			Type:   shared.ApiErrorTypeOther,
			Status: http.StatusInternalServerError,
			Msg:    fmt.Sprintf("error storing plan summary for plan %s: %v", planId, err),
		}
	}

	// latestSummaryCh <- summary

	return nil
}
```

## File: app/server/main.go
```go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"plandex-server/internal/tracing"
	"plandex-server/routes"
	"plandex-server/setup"
	"time"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	log.Println("--- RUNNING LATEST BUILD ---")
	// Configure the default logger to include milliseconds in timestamps
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.Lshortfile)

	// --- Initialize OpenTelemetry Tracer ---
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "plandex-server" // Default service name for the server
	}

	shutdownTracer, err := tracing.InitTracer(serviceName)
	if err != nil {
		log.Fatalf("❌ Failed to initialize OpenTelemetry tracer: %v", err)
	}
	// Defer shutdown with a timeout context
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second) // Give 10s for shutdown
		defer cancel()
		log.Println("[MAIN] Attempting to shut down tracer provider...")
		if err := shutdownTracer(ctx); err != nil {
			log.Printf("⚠️ Error shutting down tracer provider: %v", err)
		} else {
			log.Println("[MAIN] Tracer provider shut down successfully.")
		}
	}()

	log.Println("🚀 Plandex Server starting with tracing enabled...")

	routes.RegisterHandlePlandex(func(router *mux.Router, path string, isStreaming bool, handler routes.PlandexHandler) *mux.Route {
		return router.HandleFunc(path, handler)
	})

	r := mux.NewRouter()

	// Add a simple health endpoint with tracing demonstration
	r.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// The context r.Context() will already have trace info from otelhttp
		span := trace.SpanFromContext(r.Context())
		log.Printf("[HANDLER /health] TraceID: %s", span.SpanContext().TraceID().String())
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	routes.AddHealthRoutes(r)
	routes.AddApiRoutes(r)
	routes.AddProxyableApiRoutes(r)
	setup.MustLoadIp()
	setup.MustInitDb()

	// --- Wrap the main router with OTel HTTP instrumentation ---
	// The second argument to NewHandler is the span name for incoming requests.
	instrumentedHandler := otelhttp.NewHandler(r, "http.server.request")

	setup.StartServer(instrumentedHandler, nil, nil)
	os.Exit(0)
}
```

## File: app/server/model/client.go
```go
package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"plandex-server/types"
	"strings"
	"sync"
	"time"

	shared "plandex-shared"

	"github.com/sashabaranov/go-openai"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// note that we are *only* using streaming requests now
// non-streaming request handling has been removed completely
// streams offer more predictable cancellation partial results

const (
	ACTIVE_STREAM_CHUNK_TIMEOUT          = time.Duration(60) * time.Second
	USAGE_CHUNK_TIMEOUT                  = time.Duration(10) * time.Second
	MAX_ADDITIONAL_RETRIES_WITH_FALLBACK = 1
	MAX_RETRIES_WITHOUT_FALLBACK         = 2
	MAX_RETRY_DELAY_SECONDS              = 10
)

var httpClient = &http.Client{}

type ClientInfo struct {
	Client   *openai.Client
	ApiKey   string
	OrgId    string
	Endpoint string
}

func InitClients(apiKeys map[string]string, endpointsByApiKeyEnvVar map[string]string, openAIEndpoint, orgId string) map[string]ClientInfo {
	clients := make(map[string]ClientInfo)
	for key, apiKey := range apiKeys {
		var clientEndpoint string
		var clientOrgId string
		if key == "OPENAI_API_KEY" {
			clientEndpoint = openAIEndpoint
			clientOrgId = orgId
		} else {
			clientEndpoint = endpointsByApiKeyEnvVar[key]
		}
		clients[key] = newClient(apiKey, clientEndpoint, clientOrgId)
	}
	return clients
}

func newClient(apiKey, endpoint, orgId string) ClientInfo {
	config := openai.DefaultConfig(apiKey)
	if endpoint != "" {
		config.BaseURL = endpoint
	}
	if orgId != "" {
		config.OrgID = orgId
	}

	return ClientInfo{
		Client:   openai.NewClientWithConfig(config),
		ApiKey:   apiKey,
		OrgId:    orgId,
		Endpoint: endpoint,
	}
}

// ExtendedChatCompletionStream can wrap either a native OpenAI stream or our custom implementation
type ExtendedChatCompletionStream struct {
	openaiStream *openai.ChatCompletionStream
	customReader *StreamReader[types.ExtendedChatCompletionStreamResponse]
	ctx          context.Context
}

// StreamReader handles the SSE stream reading
type StreamReader[T any] struct {
	reader             *bufio.Reader
	response           *http.Response
	emptyMessagesLimit int
	errAccumulator     *ErrorAccumulator
	unmarshaler        *JSONUnmarshaler
}

// ErrorAccumulator keeps track of errors during streaming
type ErrorAccumulator struct {
	errors []error
	mu     sync.Mutex
}

// JSONUnmarshaler handles JSON unmarshaling for stream responses
type JSONUnmarshaler struct{}

func CreateChatCompletionStream(
	clients map[string]ClientInfo,
	modelConfig *shared.ModelRoleConfig,
	ctx context.Context,
	req types.ExtendedChatCompletionRequest,
) (*ExtendedChatCompletionStream, error) {
	_, ok := clients[modelConfig.BaseModelConfig.ApiKeyEnvVar]
	if !ok {
		fmt.Printf("client not found for api key env var: %s", modelConfig.BaseModelConfig.ApiKeyEnvVar)
		if modelConfig.MissingKeyFallback != nil {
			fmt.Println("using missing key fallback")
			return CreateChatCompletionStream(clients, modelConfig.MissingKeyFallback, ctx, req)
		}
		return nil, fmt.Errorf("client not found for api key env var: %s", modelConfig.BaseModelConfig.ApiKeyEnvVar)
	}

	resolveReq(&req, modelConfig)

	// choose the fastest provider by latency/throughput on openrouter
	if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenRouter {
		req.Model += ":nitro"
	}

	if modelConfig.BaseModelConfig.IncludeReasoning {
		req.IncludeReasoning = true
	}

	return withStreamingRetries(ctx, func(numTotalRetry int, modelErr *shared.ModelError, stripCacheControl bool) (*ExtendedChatCompletionStream, shared.FallbackResult, error) {
		fallbackRes := modelConfig.GetFallbackForModelError(numTotalRetry, modelErr)
		resolvedModelConfig := fallbackRes.ModelRoleConfig

		if resolvedModelConfig == nil {
			return nil, fallbackRes, fmt.Errorf("model config is nil")
		}

		opClient, ok := clients[resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar]

		if !ok {
			if resolvedModelConfig.MissingKeyFallback != nil {
				fmt.Println("using missing key fallback")
				resolvedModelConfig = resolvedModelConfig.MissingKeyFallback
				opClient, ok = clients[resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar]
				if !ok {
					return nil, fallbackRes, fmt.Errorf("client not found for api key env var: %s", resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar)
				}
			} else {
				return nil, fallbackRes, fmt.Errorf("client not found for api key env var: %s", resolvedModelConfig.BaseModelConfig.ApiKeyEnvVar)
			}
		}

		if stripCacheControl {
			for i := range req.Messages {
				for j := range req.Messages[i].Content {
					if req.Messages[i].Content[j].CacheControl != nil {
						req.Messages[i].Content[j].CacheControl = nil
					}
				}
			}
		}

		modelConfig = resolvedModelConfig
		resp, err := createChatCompletionStreamExtended(resolvedModelConfig, opClient, resolvedModelConfig.BaseModelConfig.BaseUrl, ctx, req)
		return resp, fallbackRes, err
	}, func(resp *ExtendedChatCompletionStream, err error) {})
}

func createChatCompletionStreamExtended(
	modelConfig *shared.ModelRoleConfig,
	client ClientInfo,
	baseUrl string,
	ctx context.Context,
	extendedReq types.ExtendedChatCompletionRequest,
) (*ExtendedChatCompletionStream, error) {
	// Start OpenTelemetry span for LLM API call
	tracer := otel.Tracer("plandex-server")
	ctx, span := tracer.Start(ctx, "llm.chat_completion_stream",
		trace.WithAttributes(
			attribute.String("llm.provider", string(modelConfig.BaseModelConfig.Provider)),
			attribute.String("llm.model", string(extendedReq.Model)),
			attribute.String("llm.api_key_env_var", modelConfig.BaseModelConfig.ApiKeyEnvVar),
			attribute.String("llm.base_url", baseUrl),
			attribute.Int("llm.message_count", len(extendedReq.Messages)),
			attribute.Float64("llm.temperature", float64(extendedReq.Temperature)),
			attribute.Float64("llm.top_p", float64(extendedReq.TopP)),
			attribute.Bool("llm.stream", extendedReq.Stream),
		),
	)
	defer span.End()

	log.Printf("LLM API call starting - Model: %s, Provider: %s (TraceID: %s)",
		extendedReq.Model, modelConfig.BaseModelConfig.Provider, span.SpanContext().TraceID().String())
	var openaiReq *types.ExtendedOpenAIChatCompletionRequest
	if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenAI && !modelConfig.BaseModelConfig.UsesOpenAIResponsesAPI {
		openaiReq = extendedReq.ToOpenAI()
		log.Println("Creating chat completion stream with direct OpenAI provider request")
	}

	// Marshal the request body to JSON
	var jsonBody []byte
	var err error
	if openaiReq != nil {
		jsonBody, err = json.Marshal(openaiReq)
	} else {
		// For custom providers, or any other case not handled by direct OpenAI,
		// we need to ensure messages are in the standard OpenAI format,
		// especially for content (string vs. array of parts).
		// The ToOpenAI() method on ExtendedChatMessage handles this.
		// We construct a temporary struct that mirrors openai.ChatCompletionRequest
		// to ensure correct marshalling.
		tempReqForMarshal := struct {
			Model            string                               `json:"model"`
			Messages         []openai.ChatCompletionMessage       `json:"messages"`
			MaxTokens        int                                  `json:"max_tokens,omitempty"`
			Temperature      float32                              `json:"temperature,omitempty"`
			TopP             float32                              `json:"top_p,omitempty"`
			N                int                                  `json:"n,omitempty"`
			Stream           bool                                 `json:"stream,omitempty"`
			Stop             []string                             `json:"stop,omitempty"`
			PresencePenalty  float32                              `json:"presence_penalty,omitempty"`
			FrequencyPenalty float32                              `json:"frequency_penalty,omitempty"`
			LogitBias        map[string]int                       `json:"logit_bias,omitempty"`
			User             string                               `json:"user,omitempty"`
			Seed             *int                                 `json:"seed,omitempty"`
			Tools            []openai.Tool                        `json:"tools,omitempty"`
			ToolChoice       interface{}                          `json:"tool_choice,omitempty"` // Can be string or object (e.g. openai.ToolChoice)
			ResponseFormat   *openai.ChatCompletionResponseFormat `json:"response_format,omitempty"`
		}{
			Model:            string(extendedReq.Model),
			MaxTokens:        extendedReq.MaxTokens,
			Temperature:      extendedReq.Temperature,
			TopP:             extendedReq.TopP,
			N:                extendedReq.N,
			Stream:           extendedReq.Stream, // This should generally be true for streaming
			Stop:             extendedReq.Stop,
			PresencePenalty:  extendedReq.PresencePenalty,
			FrequencyPenalty: extendedReq.FrequencyPenalty,
			LogitBias:        extendedReq.LogitBias,
			User:             extendedReq.User,
			Seed:             extendedReq.Seed,
			Tools:            extendedReq.Tools,
			ToolChoice:       extendedReq.ToolChoice,
			ResponseFormat:   extendedReq.ResponseFormat,
		}

		tempReqForMarshal.Messages = make([]openai.ChatCompletionMessage, len(extendedReq.Messages))
		for i, extMsg := range extendedReq.Messages {
			// extMsg is of type types.ExtendedChatMessage
			// ToOpenAI() converts it to *openai.ChatCompletionMessage,
			// correctly handling the Content field (string for simple text).
			openAIMsg := extMsg.ToOpenAI()
			if openAIMsg != nil {
				tempReqForMarshal.Messages[i] = *openAIMsg
			}
			// If openAIMsg is nil (e.g., due to an issue with extMsg),
			// it will result in a zero-value ChatCompletionMessage at that index,
			// which might need further error handling depending on API strictness.
		}
		jsonBody, err = json.Marshal(tempReqForMarshal)
	}
	if err != nil {
		return nil, fmt.Errorf("error marshaling request: %w", err)
	}

	// log.Println("request jsonBody", string(jsonBody))

	// Create new request
	var url string
	if modelConfig.BaseModelConfig.UsesOpenAIResponsesAPI {
		url = baseUrl + "/responses"
	} else {
		url = baseUrl + "/chat/completions"
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set required headers for streaming
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Authorization", "Bearer "+client.ApiKey)
	if client.OrgId != "" {
		req.Header.Set("OpenAI-Organization", client.OrgId)
	}

	// Add OpenRouter headers if it's an OpenRouter provider OR
	// if it's a Custom provider named "gemivertest" (or other OpenRouter-compatible custom providers)
	if modelConfig.BaseModelConfig.Provider == shared.ModelProviderOpenRouter ||
		(modelConfig.BaseModelConfig.Provider == shared.ModelProviderCustom &&
			modelConfig.BaseModelConfig.CustomProvider != nil &&
			(*modelConfig.BaseModelConfig.CustomProvider == "openrouter" || *modelConfig.BaseModelConfig.CustomProvider == "openrouter/auto" || *modelConfig.BaseModelConfig.CustomProvider == "gemivertest")) {
		log.Println("DEBUG: Adding OpenRouter headers for provider:", modelConfig.BaseModelConfig.Provider, "CustomProvider:", modelConfig.BaseModelConfig.CustomProvider)
		addOpenRouterHeaders(req)
	}

	// Send the request
	resp, err := httpClient.Do(req) //nolint:bodyclose // body is closed in stream.Close()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP request failed: %v", err))
		return nil, fmt.Errorf("error making request: %w", err)
	}

	// Add HTTP response attributes to span
	span.SetAttributes(
		attribute.Int("http.status_code", resp.StatusCode),
		attribute.String("http.url", url),
		attribute.String("http.method", "POST"),
	)

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, fmt.Sprintf("Failed to read error response: %v", err))
			return nil, fmt.Errorf("error reading error response: %w", err)
		}

		httpErr := &HTTPError{
			StatusCode: resp.StatusCode,
			Body:       string(body),
			Header:     resp.Header.Clone(), // retain Retry-After etc.
		}
		span.RecordError(httpErr)
		span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)))
		return nil, httpErr
	}

	// Mark span as successful
	span.SetStatus(codes.Ok, "LLM API call successful")

	// Log response headers
	// log.Println("Response headers:")
	// for key, values := range resp.Header {
	// 	log.Printf("%s: %v\n", key, values)
	// }

	reader := &StreamReader[types.ExtendedChatCompletionStreamResponse]{
		reader:             bufio.NewReader(resp.Body),
		response:           resp,
		emptyMessagesLimit: 30,
		errAccumulator:     NewErrorAccumulator(),
		unmarshaler:        &JSONUnmarshaler{},
	}

	log.Printf("LLM API call successful - Status: %d (TraceID: %s)",
		resp.StatusCode, span.SpanContext().TraceID().String())

	return &ExtendedChatCompletionStream{
		customReader: reader,
		ctx:          ctx,
	}, nil
}

func NewErrorAccumulator() *ErrorAccumulator {
	return &ErrorAccumulator{
		errors: make([]error, 0),
	}
}

func (ea *ErrorAccumulator) Add(err error) {
	ea.mu.Lock()
	defer ea.mu.Unlock()
	ea.errors = append(ea.errors, err)
}

func (ea *ErrorAccumulator) GetErrors() []error {
	ea.mu.Lock()
	defer ea.mu.Unlock()
	return ea.errors
}

func (ju *JSONUnmarshaler) Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// Recv reads from the stream
func (stream *StreamReader[T]) Recv() (*T, error) {
	for {
		line, err := stream.reader.ReadString('\n')
		if err != nil {
			return nil, err
		}

		// Trim any whitespace
		line = strings.TrimSpace(line)

		// Skip empty lines
		if line == "" {
			continue
		}

		// Check for data prefix
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Extract the data
		data := strings.TrimPrefix(line, "data: ")

		// log.Println("\n\n--- stream data:\n", data, "\n\n")

		// Check for stream completion
		if data == "[DONE]" {
			return nil, io.EOF
		}

		// Parse the response
		var response T
		err = stream.unmarshaler.Unmarshal([]byte(data), &response)
		if err != nil {
			stream.errAccumulator.Add(err)
			continue
		}

		return &response, nil
	}
}

func (stream *StreamReader[T]) Close() error {
	if stream.response != nil {
		return stream.response.Body.Close()
	}
	return nil
}

// Recv returns the next message in the stream
func (stream *ExtendedChatCompletionStream) Recv() (*types.ExtendedChatCompletionStreamResponse, error) {
	select {
	case <-stream.ctx.Done():
		return nil, stream.ctx.Err()
	default:
		if stream.openaiStream != nil {
			bytes, err := stream.openaiStream.RecvRaw()
			if err != nil {
				return nil, err
			}

			var response types.ExtendedChatCompletionStreamResponse
			err = json.Unmarshal(bytes, &response)
			if err != nil {
				return nil, err
			}
			return &response, nil
		}
		return stream.customReader.Recv()
	}
}

// Close the response body
func (stream *ExtendedChatCompletionStream) Close() error {
	if stream.openaiStream != nil {
		return stream.openaiStream.Close()
	}
	return stream.customReader.Close()
}

func resolveReq(req *types.ExtendedChatCompletionRequest, modelConfig *shared.ModelRoleConfig) {
	// if system prompt is disabled, change the role of the system message to user
	if modelConfig.BaseModelConfig.SystemPromptDisabled {
		log.Println("System prompt disabled - changing role of system message to user")
		for i, msg := range req.Messages {
			log.Println("Message role:", msg.Role)
			if msg.Role == openai.ChatMessageRoleSystem {
				log.Println("Changing role of system message to user")
				req.Messages[i].Role = openai.ChatMessageRoleUser
			}
		}

		for _, msg := range req.Messages {
			log.Println("Final message role:", msg.Role)
		}
	}

	if modelConfig.BaseModelConfig.RoleParamsDisabled {
		log.Println("Role params disabled - setting temperature and top p to 1")
		req.Temperature = 1
		req.TopP = 1
	}
}

func addOpenRouterHeaders(req *http.Request) {
	req.Header.Set("HTTP-Referer", "https://plandex.ai")
	req.Header.Set("X-Title", "Plandex")
	req.Header.Set("X-OR-Prefer", "ttft,throughput")
	if os.Getenv("GOENV") == "production" {
		req.Header.Set("X-OR-Region", "us-east-1")
	}
}
```
