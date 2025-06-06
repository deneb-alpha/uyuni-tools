// SPDX-FileCopyrightText: 2025 SUSE LLC
//
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"path"
	"testing"

	"github.com/uyuni-project/uyuni-tools/shared/testutils"
)

func TestServerInspectorGenerate(t *testing.T) {
	testDir := t.TempDir()

	inspector := NewServerInspector(testDir)
	if err := inspector.GenerateScript(); err != nil {
		t.Errorf("Unexpected error %s", err)
	}

	dataPath := inspector.GetDataPath()
	testutils.AssertEquals(t, "Invalid data path", "/var/lib/uyuni-tools/data", dataPath)

	//nolint:lll
	expected := `#!/bin/bash
# inspect.sh, generated by mgradm
echo "uyuni_release=$(cat /etc/*release | grep 'Uyuni release' | cut -d ' ' -f3 || true)" >> ` + dataPath + `
echo "suse_manager_release=$([ -f /etc/susemanager-release ] && sed 's/.*(\([0-9.]\+\).*/\1/g' /etc/susemanager-release || true)" >> ` + dataPath + `
echo "fqdn=$(sed -n '/^java\.hostname/{s/^java\.hostname[[:space:]]*=[[:space:]]*\(.*\)/\1/;p}' /etc/rhn/rhn.conf || true)" >> ` + dataPath + `
echo "current_pg_version=$((psql -V | awk '{print $3}' | cut -d. -f1) || true)" >> ` + dataPath + `
echo "current_pg_version_not_migrated=$((test -e /var/lib/pgsql/data/data/PG_VERSION && cat /var/lib/pgsql/data/data/PG_VERSION) || true)" >> ` + dataPath + `
echo "db_user=$(sed -n '/^db_user/{s/^db_user[[:space:]]*=[[:space:]]*\(.*\)/\1/;p}' /etc/rhn/rhn.conf || true)" >> ` + dataPath + `
echo "db_password=$(sed -n '/^db_password/{s/^db_password[[:space:]]*=[[:space:]]*\(.*\)/\1/;p}' /etc/rhn/rhn.conf || true)" >> ` + dataPath + `
echo "db_name=$(sed -n '/^db_name/{s/^db_name[[:space:]]*=[[:space:]]*\(.*\)/\1/;p}' /etc/rhn/rhn.conf || true)" >> ` + dataPath + `
echo "db_port=$(sed -n '/^db_port/{s/^db_port[[:space:]]*=[[:space:]]*\(.*\)/\1/;p}' /etc/rhn/rhn.conf || true)" >> ` + dataPath + `
echo "db_host=$(sed -n '/^db_host/{s/^db_host[[:space:]]*=[[:space:]]*\(.*\)/\1/;p}' /etc/rhn/rhn.conf || true)" >> ` + dataPath + `
echo "report_db_host=$(sed -n '/^report_db_host/{s/^report_db_host[[:space:]]*=[[:space:]]*\(.*\)/\1/;p}' /etc/rhn/rhn.conf || true)" >> ` + dataPath + `
exit 0
`

	actual := testutils.ReadFile(t, path.Join(testDir, InspectScriptFilename))
	testutils.AssertEquals(t, "Wrongly generated script", expected, actual)
}

func TestServerInspectorParse(t *testing.T) {
	testDir := t.TempDir()

	inspector := NewServerInspector(testDir)
	testutils.AssertEquals(t, "Invalid data path", "/var/lib/uyuni-tools/data", inspector.GetDataPath())

	// Change the data path to one we can write to during tests
	inspector.DataPath = path.Join(testDir, "data")

	content := `
uyuni_release=2024.5
suse_manager_release=5.0.0
fqdn=my.server.name
image_pg_version=16
current_pg_version=14
db_user=myuser
db_password=mysecret
db_name=mydb
db_port=1234
`
	testutils.WriteFile(t, inspector.GetDataPath(), content)

	actual, err := inspector.ReadInspectData()
	if err != nil {
		t.Fatalf("Unexpected error: %s", err)
	}

	testutils.AssertEquals(t, "Invalid uyuni release", "2024.5", actual.UyuniRelease)
	testutils.AssertEquals(t, "Invalid SUSE Manager release", "5.0.0", actual.SuseManagerRelease)
	testutils.AssertEquals(t, "Invalid FQDN", "my.server.name", actual.Fqdn)
	testutils.AssertEquals(t, "Invalid current postgresql version", "14", actual.CommonInspectData.CurrentPgVersion)
	testutils.AssertEquals(t, "Invalid image postgresql version", "16", actual.DBInspectData.ImagePgVersion)
	testutils.AssertEquals(t, "Invalid DB user", "myuser", actual.DBUser)
	testutils.AssertEquals(t, "Invalid DB password", "mysecret", actual.DBPassword)
	testutils.AssertEquals(t, "Invalid DB name", "mydb", actual.DBName)
	testutils.AssertEquals(t, "Invalid DB port", 1234, actual.DBPort)
}
