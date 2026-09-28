//go:build linux || darwin

package campaign

import (
	"errors"
	"os"
)

// CampaignIDs lists private campaign groups, including incomplete preparations.
// It does not infer completeness, inactivity or purge permission from their names.
func CampaignIDs(stateRoot string) ([]string, error) {
	return groupIDs(stateRoot, "campaigns")
}

func groupIDs(stateRoot, group string) ([]string, error) {
	r, err := os.OpenRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if err := privateDir(r, "."); err != nil {
		return nil, err
	}
	if err := privateDir(r, group); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	ids, err := directoryNames(r, group)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !validID(id) {
			return nil, ErrCorrupt
		}
		if err := privateDir(r, group+"/"+id); err != nil {
			return nil, err
		}
	}
	return ids, nil
}
