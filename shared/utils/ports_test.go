// SPDX-FileCopyrightText: 2024 SUSE LLC
//
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"testing"

	"github.com/uyuni-project/uyuni-tools/shared/testutils"
)

func TestGetServerPorts(t *testing.T) {
	allPorts := len(WebPorts) + len(ReportDBPorts) + len(SaltPorts) + len(CobblerPorts) +
		len(TaskoPorts) + len(TomcatPorts) + len(SearchPorts) + len(TftpPorts)

	ports := GetServerPorts(false)
	testutils.AssertEquals(t, "Wrong number of ports without debug ones", allPorts-3, len(ports))

	ports = GetServerPorts(true)
	testutils.AssertEquals(t, "Wrong number of ports with debug ones", allPorts, len(ports))
}
