-- Phase 3a: a chat turn can write a blog post (design 17.6).
ALTER TABLE sites.site_turns
 ADD COLUMN kind text NOT NULL DEFAULT 'page' CHECK (kind IN ('page','post')),
 ADD COLUMN post_id uuid,
 ADD FOREIGN KEY (workspace_id, site_id, post_id) REFERENCES sites.posts (workspace_id, site_id, id) ON DELETE SET NULL (post_id);
