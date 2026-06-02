package cost

import "errors"

var (
	ErrBudgetExceeded     = errors.New("cost: budget exceeded")
	ErrDailyLimitExceeded = errors.New("cost: daily limit exceeded")
	ErrTaskLimitExceeded  = errors.New("cost: task budget exceeded")
)
