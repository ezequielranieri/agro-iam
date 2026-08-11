package services

import (
	"context"
	"fmt"

	"github.com/ezequielranieri/agro-iam/internal/application/ports"
	"github.com/ezequielranieri/agro-iam/internal/domain"
)

// rolePrecedence ranks role codes from most to least privileged. A user may
// hold several memberships; the single most-privileged one is embedded in the
// access token claim at issue time (D3). The order is the decision table, so
// adding a role means editing this one list.
var rolePrecedence = []string{
	domain.RoleAdmin,
	domain.RoleAgronomist,
	domain.RoleProducer,
	domain.RoleAuditor,
	domain.RoleHauler,
}

// resolveRole returns the single most-privileged role code the user holds in
// the tenant, or "" when the user holds no memberships (an empty claim is
// legal — roleless users are allowed). A repository error propagates to the
// caller, which MUST fail closed: a role that cannot be resolved must never
// silently produce a roleless token (R13/R16). There is deliberately no
// per-request lookup beyond token-issue time.
func resolveRole(ctx context.Context, roles ports.UserRoleRepository, tenantID, userID string) (string, error) {
	memberships, err := roles.ListByUser(ctx, tenantID, userID)
	if err != nil {
		return "", fmt.Errorf("resolve role: %w", err)
	}

	held := make(map[string]bool, len(memberships))
	for _, m := range memberships {
		held[m.RoleCode] = true
	}
	for _, code := range rolePrecedence {
		if held[code] {
			return code, nil
		}
	}
	return "", nil
}
