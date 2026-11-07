package farm

import "github.com/jasonli0226/jas/core"

// farm.jsonl event types.
const (
	EvFarmPlanRefused   = "farm_plan_refused"
	EvFarmStarted       = "farm_started"
	EvPartStarted       = "part_started"
	EvPartSetup         = "part_setup"
	EvPartEnded         = "part_ended"
	EvBoardPosted       = "board_posted"
	EvBoardDelivered    = "board_delivered"
	EvOwnerDecision     = "owner_decision"
	EvIntegrateStarted  = "integrate_started"
	EvIntegrateEnded    = "integrate_ended"
	EvPartCheck         = "part_check"          // check run on a part's worktree before it is done
	EvPartCheckDeferred = "part_check_deferred" // the worker said its failing part_check needs other parts
	EvIntegrateCheck    = "integrate_check"     // check run after a clean merge, before the verdict
	EvCheckRan          = core.EvCheckRan
	EvBudgetStop        = core.EvBudgetStop
	EvFarmEnded         = "farm_ended"
)

// FarmEnded builds the payload of the final farm_ended event. exit_code and
// reason are always present; totals are merged in without overriding them.
func FarmEnded(exitCode int, reason string, totals map[string]any) map[string]any {
	p := make(map[string]any, len(totals)+2)
	for k, v := range totals {
		p[k] = v
	}
	p["exit_code"] = exitCode
	p["reason"] = reason
	return p
}
