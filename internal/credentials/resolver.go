package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"follower/internal/domain"
)

type envLookup func(string) (string, bool)
type fileRead func(string) ([]byte, error)

type Resolver struct {
	lookupEnv envLookup
	readFile  fileRead
}

func NewResolver() *Resolver {
	return &Resolver{
		lookupEnv: os.LookupEnv,
		readFile:  os.ReadFile,
	}
}

func NewResolverWithDeps(lookup envLookup, read fileRead) *Resolver {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	if read == nil {
		read = os.ReadFile
	}
	return &Resolver{
		lookupEnv: lookup,
		readFile:  read,
	}
}

func (r *Resolver) Resolve(
	ctx context.Context,
	source domain.CredentialSource,
	reference string,
) (domain.AccountCredentials, error) {
	if err := ctx.Err(); err != nil {
		return domain.AccountCredentials{}, err
	}

	normalizedSource := domain.NormalizeCredentialSource(source)
	normalizedReference := strings.TrimSpace(reference)
	if normalizedReference == "" {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"credential reference must not be empty",
		)
	}

	switch normalizedSource {
	case domain.CredentialSourceEnv:
		return r.resolveFromEnv(normalizedReference)
	case domain.CredentialSourceFile:
		return r.resolveFromFile(normalizedReference)
	case domain.CredentialSourceVault:
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"credential source vault is not configured",
		)
	default:
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthChallengeBlocked,
			"credential source manual requires operator interaction",
		)
	}
}

func (r *Resolver) resolveFromEnv(reference string) (domain.AccountCredentials, error) {
	ref := strings.TrimPrefix(reference, "env://")
	parts := splitCredentialRefPair(ref)
	if len(parts) != 2 {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"env credential reference must declare username/password variable names",
		)
	}

	usernameVar := strings.TrimSpace(parts[0])
	passwordVar := strings.TrimSpace(parts[1])
	if usernameVar == "" || passwordVar == "" {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"env credential variable names must not be empty",
		)
	}

	username, ok := r.lookupEnv(usernameVar)
	if !ok || strings.TrimSpace(username) == "" {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			fmt.Sprintf("env credential variable %s is missing", usernameVar),
		)
	}
	password, ok := r.lookupEnv(passwordVar)
	if !ok || strings.TrimSpace(password) == "" {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			fmt.Sprintf("env credential variable %s is missing", passwordVar),
		)
	}

	credentials := domain.AccountCredentials{
		Username: username,
		Password: password,
	}
	if err := credentials.Validate(); err != nil {
		return domain.AccountCredentials{}, err
	}

	return credentials, nil
}

func (r *Resolver) resolveFromFile(reference string) (domain.AccountCredentials, error) {
	path := strings.TrimPrefix(reference, "file://")
	path = strings.TrimSpace(path)
	if path == "" {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"file credential path must not be empty",
		)
	}

	payload, err := r.readFile(path)
	if err != nil {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"cannot read credential file",
		)
	}

	var raw struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Login    string `json:"login"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return domain.AccountCredentials{}, domain.NewDomainError(
			domain.ErrorCodeAuthBootstrapFailed,
			"credential file must be valid JSON",
		)
	}

	username := strings.TrimSpace(raw.Username)
	if username == "" {
		username = strings.TrimSpace(raw.Login)
	}

	credentials := domain.AccountCredentials{
		Username: username,
		Password: raw.Password,
	}
	if err := credentials.Validate(); err != nil {
		return domain.AccountCredentials{}, err
	}

	return credentials, nil
}

func splitCredentialRefPair(reference string) []string {
	if strings.Contains(reference, ",") {
		return strings.SplitN(reference, ",", 2)
	}
	return strings.SplitN(reference, ":", 2)
}
