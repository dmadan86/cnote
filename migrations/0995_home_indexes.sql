-- Indexes for the home dashboard (home.summary): pending confirmations and open deals by stage.
CREATE INDEX IF NOT EXISTS confirmations_pending ON core.confirmations(workspace_id, created_at DESC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS deals_ws_stage ON crm.deals(workspace_id, stage);
