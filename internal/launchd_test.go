//go:build darwin

package internal

import (
	"bytes"
	"strings"
	"testing"
)

func TestLaunchdUsesPrivateGatewayEntryPoint(t *testing.T) {
	var out bytes.Buffer
	data := struct {
		Label, Binary, LogPath string
	}{"com.aix.proxy", "/opt/homebrew/bin/aix", "/tmp/aix.log"}
	if err := plistTmpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	plist := out.String()
	if !strings.Contains(plist, "<key>AIX_INTERNAL_PROXY</key>") {
		t.Fatalf("private gateway environment is missing:\n%s", plist)
	}
	for _, oldArg := range []string{"<string>proxy</string>", "<string>start</string>", "<string>--foreground</string>"} {
		if strings.Contains(plist, oldArg) {
			t.Errorf("launchd still uses removed CLI argument %s", oldArg)
		}
	}
}
