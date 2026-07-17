// SPDX-License-Identifier: AGPL-3.0-only

package egress

import (
	"context"

	"github.com/openbox-dev/openbox/internal/domain"
)

// RefreshReport summarizes a DNS-driven allowlist ACL re-apply pass.
type RefreshReport struct {
	Refreshed int
	Skipped   int
	Errors    []ApplyError
}

// RefreshHostnameAllowlists re-applies restricted profiles that include
// hostname allowlist entries so DNS TTL changes converge into Incus ACLs.
func (s *Service) RefreshHostnameAllowlists(ctx context.Context) (RefreshReport, error) {
	var report RefreshReport
	if s.applicator == nil {
		return report, nil
	}
	profiles, err := s.store.ListEgressProfiles(ctx)
	if err != nil {
		return report, err
	}
	for _, profile := range profiles {
		if profile.Mode != domain.EgressRestricted {
			continue
		}
		_, hostnames, parseErr := domain.ParseAllowlistEntries(profile.AllowedDestinationsJSON)
		if parseErr != nil || len(hostnames) == 0 {
			continue
		}
		instances, listErr := s.store.ListInstancesWithEgressProfile(ctx, profile.ID)
		if listErr != nil {
			report.Errors = append(report.Errors, ApplyError{Message: listErr.Error()})
			continue
		}
		for _, instance := range instances {
			if instance.RuntimeRef == "" {
				report.Skipped++
				continue
			}
			instance.EgressMode = profile.Mode
			instance.EgressProfileID = profile.ID
			// Refresh failures must not mark the instance error: the previous ACL
			// may still be correct. Record audit + apply_errors only.
			changed, err := s.applicator.apply(ctx, instance, profile, false)
			if err != nil {
				if s.auditor != nil {
					_ = s.auditor.RecordPolicyEvent(ctx, PolicyAuditEvent{
						OwnerID: instance.OwnerID, Actor: "openboxd",
						Action: ActionPolicyRefreshFailed, InstanceID: instance.ID,
						ProfileID: profile.ID, Mode: profile.Mode, Outcome: "failed",
						Message: err.Error(),
					})
				}
				report.Errors = append(report.Errors, ApplyError{InstanceID: instance.ID, Message: err.Error()})
				continue
			}
			if !changed {
				report.Skipped++
				continue
			}
			if s.auditor != nil {
				_ = s.auditor.RecordPolicyEvent(ctx, PolicyAuditEvent{
					OwnerID: instance.OwnerID, Actor: "openboxd",
					Action: ActionPolicyRefresh, InstanceID: instance.ID,
					ProfileID: profile.ID, Mode: profile.Mode, Outcome: "succeeded",
				})
			}
			report.Refreshed++
		}
	}
	return report, nil
}
