//go:build !linux

package kvm

import (
	"context"
	"errors"
	"io"
	"os/user"
)

// Grant is for Linux: elsewhere AgentBox's VM doesn't use KVM.
func Grant(context.Context, *user.User, io.Writer) error {
	return errors.New("only Linux has /dev/kvm")
}
