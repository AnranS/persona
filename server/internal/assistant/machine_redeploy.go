package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type machineRedeployer interface {
	Redeploy(ctx context.Context, id string) error
}

func (s *Service) toolMachineRedeploy(ctx context.Context, args json.RawMessage) (string, error) {
	var a struct {
		Machine string `json:"machine"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", err
	}
	selector := strings.TrimSpace(a.Machine)
	if selector == "" {
		return "", fmt.Errorf("machine is required")
	}
	inventory := s.devices(ctx)
	var target Device
	found := false
	for _, machine := range inventory {
		if machine.ID == selector {
			target, found = machine, true
			break
		}
	}
	if !found {
		for _, machine := range inventory {
			if machine.Name != selector {
				continue
			}
			if found {
				return "", fmt.Errorf("machine name %q is ambiguous; use its ID", selector)
			}
			target, found = machine, true
		}
	}
	if !found {
		return "", fmt.Errorf("no machine %q", selector)
	}
	d, ok := s.devs.(machineRedeployer)
	if !ok {
		return "", fmt.Errorf("machine redeployment is unavailable")
	}
	if err := d.Redeploy(ctx, target.ID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Redeploy of %s (%s) was successful.", target.Name, target.ID), nil
}
