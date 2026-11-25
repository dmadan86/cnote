-- Marketplace uninstalls move from settings.modules (an allow-list) to settings.hidden_modules (a deny-list).
-- The old list was written by two things that cannot be told apart afterwards: Marketplace toggles and business-type
-- presets (docs, projects, crm, billing; the old setup step also let people tick whiteboard). The Marketplace keeps no
-- install table or audit row for official modules, and core.events records only the settings keys, not their values.
-- So there is no reliable evidence, and this uses the safe rule: hide a module only if it is a Marketplace-uninstallable
-- app (docs, projects, crm, billing, whiteboard), it is absent from an existing `modules` list, and presets never
-- manage it. Only whiteboard qualifies: it was hidden by that list before, so hiding it keeps what people saw.
-- docs, projects, crm and billing stay visible (absence may just be a preset), and tables and website stay visible
-- (presets wrongly hid them). Workspaces that already have hidden_modules are left alone.
UPDATE core.workspaces
   SET settings = settings || jsonb_build_object('hidden_modules', jsonb_build_array('whiteboard'))
 WHERE jsonb_typeof(settings -> 'modules') = 'array'
   AND NOT (settings ? 'hidden_modules')
   AND NOT ((settings -> 'modules') ? 'whiteboard');
