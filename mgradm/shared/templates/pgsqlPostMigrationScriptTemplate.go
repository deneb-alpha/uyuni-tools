// SPDX-FileCopyrightText: 2023 SUSE LLC
//
// SPDX-License-Identifier: Apache-2.0

package templates

import (
	"io"
	"text/template"
)

const postgresFinalizeScriptTemplate = `#!/bin/bash
set -e

{{ if .RunAutotune }}
echo "Running smdba system-check autotuning..."
smdba system-check autotuning
{{ end }}
echo "Starting Postgresql..."
su -s /bin/bash - postgres -c "/usr/share/postgresql/postgresql-script start"
{{ if .RunReindex }}
echo "Reindexing database. This may take a while, please do not cancel it!"
database=$(sed -n "s/^\s*db_name\s*=\s*\([^ ]*\)\s*$/\1/p" /etc/rhn/rhn.conf)
spacewalk-sql --select-mode - <<<"REINDEX DATABASE \"${database}\";"
{{ end }}

{{ if .RunSchemaUpdate }}
echo "Schema update..."
/usr/sbin/spacewalk-startup-helper check-database
{{ end }}

{{ if .RunDistroMigration }}
echo "Updating auto-installable distributions..."
spacewalk-sql --select-mode - <<EOT
SELECT MIN(CONCAT(org_id, '-', label)) AS target, base_path INTO TEMP TABLE dist_map FROM rhnKickstartableTree GROUP BY base_path;
UPDATE rhnKickstartableTree SET base_path = CONCAT('/srv/www/distributions/', target)
    from dist_map WHERE dist_map.base_path = rhnKickstartableTree.base_path;
DROP TABLE dist_map;
EOT
{{ end }}
echo "Stopping Postgresql..."
su -s /bin/bash - postgres -c "/usr/share/postgresql/postgresql-script stop"
echo "DONE"
`

type FinalizePostgresTemplateData struct {
	RunAutotune        bool
	RunReindex         bool
	RunSchemaUpdate    bool
	RunDistroMigration bool
	Kubernetes         bool
}

func (data FinalizePostgresTemplateData) Render(wr io.Writer) error {
	t := template.Must(template.New("script").Parse(postgresFinalizeScriptTemplate))
	return t.Execute(wr, data)
}