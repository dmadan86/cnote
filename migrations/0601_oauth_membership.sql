-- Removing someone from a workspace ends every OAuth grant they approved there, whatever code path removes
-- them, so re-adding them later cannot resurrect old tokens.
CREATE OR REPLACE FUNCTION core.revoke_oauth_on_member_removal() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = core, pg_temp AS $$
BEGIN
  UPDATE core.oauth_grants SET revoked_at = coalesce(revoked_at, now())
   WHERE user_id = OLD.user_id AND workspace_id = OLD.workspace_id AND revoked_at IS NULL;
  DELETE FROM core.oauth_tokens WHERE grant_id IN
    (SELECT id FROM core.oauth_grants WHERE user_id = OLD.user_id AND workspace_id = OLD.workspace_id);
  RETURN OLD;
END $$;
CREATE TRIGGER oauth_revoke_on_member_removal AFTER DELETE ON core.memberships
  FOR EACH ROW EXECUTE FUNCTION core.revoke_oauth_on_member_removal();
