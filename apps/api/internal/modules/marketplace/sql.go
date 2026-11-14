package marketplace

const platformEmailSQL = `select lower(email) from core.users where id = $1`
const listSQL = `select jsonb_build_object('id', a.id, 'slug', a.slug, 'name', a.name, 'tagline', a.tagline,
           'category', a.category, 'icon_url', a.icon_url, 'publisher_name', a.publisher_name, 'scopes', a.scopes,
           'installed', exists(select 1 from core.app_installs i where i.app_id = a.id),
           'installs', core.app_install_count(a.id))
         from core.market_apps a
         where a.status = 'published' and ($1 = '' or a.name ilike $1 or a.tagline ilike $1 or a.slug ilike $1)
         order by core.app_install_count(a.id) desc, a.name`
const getSQL = `select jsonb_build_object('id', a.id, 'slug', a.slug, 'name', a.name, 'tagline', a.tagline,
           'description', a.description, 'category', a.category, 'icon_url', a.icon_url,
           'homepage_url', a.homepage_url, 'publisher_name', a.publisher_name, 'scopes', a.scopes,
           'status', a.status, 'installed', exists(select 1 from core.app_installs i where i.app_id = a.id),
           'installs', core.app_install_count(a.id))
         from core.market_apps a
         where a.slug = $1 and (a.status = 'published' or a.publisher_workspace_id = $2)`
const settingsSQL = `select settings from core.workspaces where id = $1`
const installAppSQL = `select name, redirect_url, scopes from core.market_apps
         where id = $1 and (status = 'published' or publisher_workspace_id = $2)`
const installedExistsSQL = `select exists(select 1 from core.app_installs where app_id = $1)`
const insertAgentSQL = `insert into core.agents (workspace_id, name, kind, owner_user_id, scopes) values ($1,$2,'external',$3,$4) returning id`
const insertInstallSQL = `insert into core.app_installs (workspace_id, app_id, agent_id, installed_by, scopes) values ($1,$2,$3,$4,$5) returning id`
const insertCodeSQL = `insert into core.app_install_codes (code_hash, install_id, workspace_id, expires_at)
         values ($1,$2,$3, now() + make_interval(mins => $4))`
const uninstallSQL = `delete from core.agents where id = (select agent_id from core.app_installs where app_id = $1)`
const installedSQL = `select jsonb_build_object('app_id', a.id, 'slug', a.slug, 'name', a.name, 'icon_url', a.icon_url,
           'publisher_name', a.publisher_name, 'scopes', i.scopes, 'installed_at', i.created_at,
           'installed_by_name', u.name)
         from core.app_installs i join core.market_apps a on a.id = i.app_id join core.users u on u.id = i.installed_by
         order by a.name`
const fullAppSQL = `select to_jsonb(a) - 'client_secret_hash' || jsonb_build_object('installs', core.app_install_count(a.id)) `
const slugTakenSQL = `select exists(select 1 from core.market_apps where slug = $1 and id is distinct from $2)`
const updateAppSQL = `update core.market_apps set slug=$3, name=$4, tagline=$5, description=$6, category=$7, icon_url=$8,
               homepage_url=$9, redirect_url=$10, scopes=$11, updated_at=now(),
               status = case when status = 'published' then 'review' else status end
             where id = $1 and publisher_workspace_id = $2`
const publisherSQL = `select name from core.workspaces where id = $1`
const insertAppSQL = `insert into core.market_apps (slug, name, tagline, description, category, icon_url, homepage_url, redirect_url,
           scopes, publisher_workspace_id, publisher_name, client_secret_hash)
         values ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) returning id`
const submitSQL = `update core.market_apps set status = 'review', updated_at = now()
         where id = $1 and publisher_workspace_id = $2 and status in ('draft','rejected')`
const rotateSecretSQL = `update core.market_apps set client_secret_hash = $3, updated_at = now() where id = $1 and publisher_workspace_id = $2`
const installCountSQL = `select core.app_install_count(id) from core.market_apps where id = $1 and publisher_workspace_id = $2`
const deleteAppSQL = `delete from core.market_apps where id = $1`
const reviewSQL = `update core.market_apps set status = $2, review_note = $3, updated_at = now() where id = $1 and status = 'review'`
