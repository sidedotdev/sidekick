package workflow

type Context interface {
	Done()
}

func Go(ctx Context, f func(Context)) {}
