package common

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestAndroidCommandPermissions(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../side.yml")
	require.NoError(t, err)
	var config struct {
		Permissions struct {
			RequireApproval []struct {
				Pattern string `yaml:"pattern"`
			} `yaml:"require_approval"`
			AutoApprove []struct {
				Pattern string `yaml:"pattern"`
			} `yaml:"auto_approve"`
		} `yaml:"command_permissions"`
	}
	require.NoError(t, yaml.Unmarshal(data, &config))
	var permissions CommandPermissionConfig
	for _, rule := range config.Permissions.RequireApproval {
		permissions.RequireApproval = append(permissions.RequireApproval, CommandPattern{Pattern: rule.Pattern})
	}
	for _, rule := range config.Permissions.AutoApprove {
		if strings.Contains(rule.Pattern, "gradlew") || strings.Contains(rule.Pattern, "android_phone_remote_e2e") {
			permissions.AutoApprove = append(permissions.AutoApprove, CommandPattern{Pattern: rule.Pattern})
		}
	}
	require.Len(t, permissions.AutoApprove, 3)
	for _, command := range []string{
		"app/android/gradlew -p app/android :app:testDebugUnitTest --tests 'com.example.*'",
		"./app/android/gradlew -p app/android :app:assembleDebug --no-configuration-cache",
		"./gradlew :app:lintDebug --offline",
		"scripts/android_phone_remote_e2e/run.sh",
		"SIDE_SERVER_PORT=8856 scripts/android_phone_remote_e2e/run.sh -s 'adb-phone._adb-tls-connect._tcp' -t 900",
	} {
		t.Run(command, func(t *testing.T) {
			result, _ := EvaluateCommandPermission(permissions, command)
			require.Equal(t, PermissionAutoApprove, result)
			result = EvaluateScriptPermissionDetailed(permissions, command, EvaluatePermissionOptions{
				StripEnvVarPrefix: true,
			}).Outcome
			require.Equal(t, PermissionAutoApprove, result)
		})
	}
	for _, command := range []string{
		"GRADLE_OPTS='-Dorg.gradle.project.initScript=evil.gradle' ./gradlew :app:check",
		"JAVA_TOOL_OPTIONS='-javaagent:evil.jar' ./gradlew :app:check",
		"GRADLE_USER_HOME=elsewhere ./gradlew :app:check",
		"./gradlew :app:check -I evil.gradle",
		"./gradlew :app:check -b evil.gradle",
		"./gradlew :app:check -p elsewhere",
		"./gradlew :app:check --settings-file evil.gradle",
		"./gradlew :app:check; malicious-command",
		"./gradlew :app:check $(malicious-command)",
		"scripts/android_phone_remote_e2e/run.sh -s \"$(malicious-command)\"",
		"scripts/android_phone_remote_e2e/run.sh && malicious-command",
	} {
		t.Run(command, func(t *testing.T) {
			result, _ := EvaluateCommandPermission(permissions, command)
			require.NotEqual(t, PermissionAutoApprove, result)
			result = EvaluateScriptPermissionDetailed(permissions, command, EvaluatePermissionOptions{
				StripEnvVarPrefix: true,
			}).Outcome
			require.NotEqual(t, PermissionAutoApprove, result)
		})
	}
}
