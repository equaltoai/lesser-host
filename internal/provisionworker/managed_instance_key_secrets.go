package provisionworker

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"

	"github.com/equaltoai/lesser-host/internal/store/models"
)

type managedInstanceSecretsInputs struct {
	accountID string
	roleName  string
	region    string
	slug      string
	jobID     string
}

func managedInstanceSecretsInputsFromJob(job *models.ProvisionJob) (managedInstanceSecretsInputs, error) {
	if job == nil {
		return managedInstanceSecretsInputs{}, fmt.Errorf("job is required")
	}

	inputs := managedInstanceSecretsInputs{
		accountID: strings.TrimSpace(job.AccountID),
		roleName:  strings.TrimSpace(job.AccountRoleName),
		region:    strings.TrimSpace(job.Region),
		slug:      strings.ToLower(strings.TrimSpace(job.InstanceSlug)),
		jobID:     strings.TrimSpace(job.ID),
	}
	if inputs.accountID == "" || inputs.roleName == "" || inputs.slug == "" || inputs.jobID == "" {
		return managedInstanceSecretsInputs{}, fmt.Errorf("missing required provisioning inputs")
	}
	return inputs, nil
}

func getSecretsManagerSecretPlaintext(ctx context.Context, sm secretsManagerAPI, secretArn string) (string, error) {
	secretArn = strings.TrimSpace(secretArn)
	if secretArn == "" {
		return "", fmt.Errorf("secret arn is required")
	}

	out, err := sm.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretArn)})
	if err != nil {
		return "", fmt.Errorf("get secret value: %w", err)
	}

	raw := strings.TrimSpace(aws.ToString(out.SecretString))
	if raw == "" && len(out.SecretBinary) > 0 {
		raw = strings.TrimSpace(string(out.SecretBinary))
	}
	plaintext, err := unwrapSecretsManagerSecretString(raw)
	if err != nil {
		return "", fmt.Errorf("parse secret value: %w", err)
	}
	return plaintext, nil
}

func generateInstanceKeySecret() (string, string, string, error) {
	secretToken, err := newToken(32)
	if err != nil {
		return "", "", "", err
	}
	plaintext := "lhk_" + secretToken
	keyID := secretValueToKeyID(plaintext)
	if keyID == "" {
		return "", "", "", fmt.Errorf("failed to derive instance key id")
	}

	secretJSON, err := wrapSecretsManagerSecretString(plaintext)
	if err != nil {
		return "", "", "", err
	}
	return plaintext, keyID, secretJSON, nil
}

func (s *Server) describeAndEnsureManagedInstanceKeySecret(ctx context.Context, sm secretsManagerAPI, slug string, secretID string) (string, error) {
	if err := s.requireStoreDB(); err != nil {
		return "", err
	}
	if sm == nil {
		return "", fmt.Errorf("secrets manager client not initialized")
	}

	slug = strings.ToLower(strings.TrimSpace(slug))
	secretID = strings.TrimSpace(secretID)
	if slug == "" || secretID == "" {
		return "", fmt.Errorf("slug and secretID are required")
	}

	desc, describeErr := sm.DescribeSecret(ctx, &secretsmanager.DescribeSecretInput{SecretId: aws.String(secretID)})
	if describeErr != nil {
		return "", describeErr
	}

	secretArn := strings.TrimSpace(aws.ToString(desc.ARN))
	if secretArn == "" {
		secretArn = secretID
	}

	keyID := secretsManagerTagValue(desc.Tags, managedInstanceKeySecretTagKeyID)
	if keyID == "" {
		plaintext, err := getSecretsManagerSecretPlaintext(ctx, sm, secretArn)
		if err != nil {
			return "", err
		}
		keyID = secretValueToKeyID(plaintext)
	}
	if keyID == "" {
		return "", fmt.Errorf("unable to resolve instance key id from secret")
	}

	if err := s.ensureInstanceKeyRecord(ctx, slug, keyID); err != nil {
		return "", fmt.Errorf("ensure instance key record: %w", err)
	}

	return secretArn, nil
}

func (s *Server) createManagedInstanceKeySecret(ctx context.Context, sm secretsManagerAPI, secretName, slug string) (string, string, error) {
	if sm == nil {
		return "", "", fmt.Errorf("secrets manager client not initialized")
	}
	secretName = strings.TrimSpace(secretName)
	slug = strings.ToLower(strings.TrimSpace(slug))
	if secretName == "" || slug == "" {
		return "", "", fmt.Errorf("secret name and slug are required")
	}

	_, keyID, secretJSON, err := generateInstanceKeySecret()
	if err != nil {
		return "", "", err
	}

	createOut, err := sm.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
		Name:         aws.String(secretName),
		Description:  aws.String("lesser.host managed instance API key"),
		SecretString: aws.String(secretJSON),
		Tags: []smtypes.Tag{
			{Key: aws.String(managedInstanceKeySecretTagInstanceSlug), Value: aws.String(slug)},
			{Key: aws.String(managedInstanceKeySecretTagKeyID), Value: aws.String(keyID)},
			{Key: aws.String(managedInstanceKeySecretTagManaged), Value: aws.String("true")},
		},
	})
	if err != nil {
		return "", "", err
	}

	arn := strings.TrimSpace(aws.ToString(createOut.ARN))
	return arn, keyID, nil
}

func updateManagedInstanceKeySecretTags(ctx context.Context, sm secretsManagerAPI, secretArn, slug, keyID string) {
	if sm == nil {
		return
	}
	secretArn = strings.TrimSpace(secretArn)
	slug = strings.ToLower(strings.TrimSpace(slug))
	keyID = strings.TrimSpace(keyID)
	if secretArn == "" || slug == "" || keyID == "" {
		return
	}

	// Ensure the secret tags reflect the current key id so future ensures can resolve the key id
	// without fetching the secret value.
	_, _ = sm.UntagResource(ctx, &secretsmanager.UntagResourceInput{
		SecretId: aws.String(secretArn),
		TagKeys:  []string{managedInstanceKeySecretTagKeyID},
	})
	_, _ = sm.TagResource(ctx, &secretsmanager.TagResourceInput{
		SecretId: aws.String(secretArn),
		Tags: []smtypes.Tag{
			{Key: aws.String(managedInstanceKeySecretTagInstanceSlug), Value: aws.String(slug)},
			{Key: aws.String(managedInstanceKeySecretTagKeyID), Value: aws.String(keyID)},
			{Key: aws.String(managedInstanceKeySecretTagManaged), Value: aws.String("true")},
		},
	})
}
