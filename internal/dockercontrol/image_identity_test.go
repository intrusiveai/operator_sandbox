//go:build linux || darwin

package dockercontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"text/template"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

func TestVersionedContainerImageIdentity(t *testing.T) {
	for _, store := range []string{"classic", "containerd"} {
		for _, version := range []string{campaign.BindingVersion, campaign.SelectedImageBindingVersion} {
			t.Run(store+"/"+version, func(t *testing.T) {
				b := binding()
				b.APIVersion = version
				selected := b.ImageDigest
				top := selected
				if store == "containerd" {
					top = "sha256:" + strings.Repeat("c", 64)
				}
				object := map[string]any{"Id": b.DockerContainerID, "Image": top, "Config": map[string]any{"Labels": b.Labels}, "State": map[string]any{"Status": "created", "Running": false, "Paused": false, "Restarting": false}, "HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": "no"}, "AutoRemove": false}}
				if store == "containerd" {
					object["ImageManifestDescriptor"] = map[string]any{"digest": selected}
				}
				c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
					if args[0] == "info" {
						return json.Marshal(b.DaemonID)
					}
					if args[0] != "container" || args[1] != "inspect" || args[len(args)-1] != b.DockerContainerID {
						t.Fatal(args)
					}
					format := args[3]
					if version == campaign.BindingVersion && format != inspectFormat {
						t.Fatal("legacy semantics changed")
					}
					tmpl, err := template.New("docker").Funcs(template.FuncMap{"json": func(v any) string { raw, _ := json.Marshal(v); return string(raw) }}).Parse(format)
					if err != nil {
						t.Fatal(err)
					}
					var out bytes.Buffer
					if err = tmpl.Execute(&out, object); err != nil {
						t.Fatal(err)
					}
					return out.Bytes(), nil
				}}
				_, code := c.inspect(context.Background(), b)
				if version == campaign.BindingVersion && store == "containerd" {
					if code != "identity_mismatch" {
						t.Fatal("legacy binding reinterpreted", code)
					}
				} else if code != "" {
					t.Fatal(code)
				}
				if store == "containerd" && version == campaign.SelectedImageBindingVersion {
					object["ImageManifestDescriptor"] = map[string]any{"digest": "sha256:" + strings.Repeat("d", 64)}
					if _, code = c.inspect(context.Background(), b); code != "identity_mismatch" {
						t.Fatal("selected image mismatch accepted", code)
					}
				}
			})
		}
	}
}
