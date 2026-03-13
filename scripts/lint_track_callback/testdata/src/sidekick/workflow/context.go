package workflow

type Context interface{}

func ExecuteActivity(ctx Context, activity interface{}, args ...interface{}) {}