-- Per-person screen settings (home widgets, list columns). One row per workspace, person and screen; no row = the screen's default.
-- A config only names widgets and columns, it never grants access: every data command still checks the caller.
-- The shape of config is the web's business; the server bounds size and ownership only (modules/uiconfig).
CREATE TABLE core.ui_configs (
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  user_id      uuid NOT NULL REFERENCES core.users(id) ON DELETE CASCADE,
  screen       text NOT NULL CHECK (screen ~ '^[a-z0-9_.:-]{1,64}$'),
  config       jsonb NOT NULL CHECK (jsonb_typeof(config) = 'object' AND octet_length(config::text) <= 16384),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace_id, user_id, screen)
);

SELECT core.enable_tenant_rls('core.ui_configs');
GRANT SELECT, INSERT, UPDATE, DELETE ON core.ui_configs TO cnote_app;
