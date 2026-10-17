package task

// checkStatusRule decides whether actor may move t to status. The operator
// may make any move. Refusals say what the actor can do instead.
func checkStatusRule(actor string, t *Task, status, reason string) error {
	if status == StatusBlocked && reason == "" {
		return ruleErr("say why %s is blocked: pass a reason.", t.Key())
	}
	if actor == Human {
		return nil
	}
	if t.Closed() {
		return ruleErr("%s is %s. Only the operator can reopen it.", t.Key(), t.Status)
	}
	worker := actor == t.Holder || actor == t.Assignee
	switch status {
	case StatusInProgress:
		return ruleErr("claim %s with task_checkout to start work on it.", t.Key())
	case StatusBlocked, StatusReview:
		if !worker {
			return ruleErr("only the assignee can move %s to %s. Comment instead.", t.Key(), status)
		}
	case StatusTodo:
		if !worker && actor != t.Reviewer {
			return ruleErr("only the assignee or the reviewer can send %s back to To do. Comment instead.", t.Key())
		}
	case StatusDone:
		return checkDoneRule(actor, t, worker)
	case StatusCancelled:
		if actor != t.CreatedBy {
			return ruleErr("only the filer (%s) or the operator can cancel %s.", MemberName(t.CreatedBy), t.Key())
		}
	}
	return nil
}

// checkDoneRule enforces review: with a reviewer set, only the reviewer
// closes the task, and the assignee moves it to Review instead.
func checkDoneRule(actor string, t *Task, worker bool) error {
	if t.Reviewer == "" {
		if worker || actor == t.CreatedBy {
			return nil
		}
		return ruleErr("only the assignee or the filer can close %s. Comment instead.", t.Key())
	}
	if actor == t.Reviewer {
		return nil
	}
	if worker {
		return ruleErr("%s needs review by %s. Move it to review and they'll close it.", t.Key(), MemberName(t.Reviewer))
	}
	return ruleErr("only the reviewer (%s) can close %s.", MemberName(t.Reviewer), t.Key())
}
