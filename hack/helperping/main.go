// SPDX-License-Identifier: BSD-3-Clause

// Command helperping asks the installed helper for its status over its
// socket, as the app does, and exits non-zero if it does not answer. CI
// runs it after install.ps1 to see the service up, not merely installed.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/claimward/claimward-vpn-client/pkg/helperclient"
)

func main() {
	resp, err := helperclient.New("").Status()
	if err != nil {
		fmt.Fprintln(os.Stderr, "helperping:", err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(resp)
	if !resp.OK {
		os.Exit(1)
	}
}
