package env

import (
	"context"
	"errors"
)

// withModalAdmission holds admission across work on separately pooled channels,
// preventing shutdown between admission and dispatch. Only acquiring the lease
// may retry; op is invoked at most once.
func withModalAdmission(ctx context.Context, e *ModalEnv, op func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	release, err := BeginActivity(ctx, e, cancel)
	if err != nil {
		return err
	}
	defer release()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	err = op(ctx)
	if cause := context.Cause(ctx); cause != nil {
		return errors.Join(err, cause)
	}
	return err
}

type modalFileTransport struct {
	SSHTransport
	env *ModalEnv
}

func (t modalFileTransport) WithSFTP(ctx context.Context, op SFTPOp) (value any, err error) {
	err = withModalAdmission(ctx, t.env, func(ctx context.Context) error {
		var opErr error
		value, opErr = t.SSHTransport.WithSFTP(ctx, op)
		return opErr
	})
	return value, err
}
