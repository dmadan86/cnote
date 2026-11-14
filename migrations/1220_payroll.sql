-- WP25 payroll. Money is integer paise. Statutory rates live in global, dated config tables (payroll.params,
-- tax_slabs, pt_slabs): the app role can only read them, a CA or DBA changes a rate by inserting a row with a newer
-- effective_from (no deploy). Every seeded value is marked "CA review". Workspace tables are tenant tables (RLS, composite FKs).
CREATE SCHEMA IF NOT EXISTS payroll;
GRANT USAGE ON SCHEMA payroll TO cnote_app;

-- ---------- statutory configuration (global) ----------
CREATE TABLE payroll.params (
  key text NOT NULL,
  effective_from date NOT NULL,
  value bigint NOT NULL,            -- paise for *_minor keys, basis points for *_bp keys
  source text NOT NULL,
  note text NOT NULL DEFAULT 'CA review',
  PRIMARY KEY (key, effective_from)
);
CREATE TABLE payroll.tax_slabs (       -- new regime slabs on annual taxable income; a slab starts at from_minor
  regime text NOT NULL CHECK (regime IN ('new')),
  effective_from date NOT NULL,
  from_minor bigint NOT NULL CHECK (from_minor >= 0),
  rate_bp integer NOT NULL CHECK (rate_bp BETWEEN 0 AND 10000),
  source text NOT NULL,
  PRIMARY KEY (regime, effective_from, from_minor)
);
CREATE TABLE payroll.pt_slabs (        -- professional tax: the first row (by upto_minor) whose bound covers monthly gross x basis_months
  state_code text NOT NULL,            -- GST state code
  effective_from date NOT NULL,
  upto_minor bigint,                   -- inclusive upper bound; null = no upper bound
  amount_minor bigint NOT NULL CHECK (amount_minor >= 0),
  feb_amount_minor bigint CHECK (feb_amount_minor >= 0),   -- February override (Karnataka, Maharashtra)
  basis_months integer NOT NULL DEFAULT 1 CHECK (basis_months > 0),  -- Tamil Nadu slabs are on half-year pay
  months integer[],                    -- months (1-12) the tax is deducted in; null = every month
  state_name text NOT NULL,
  source text NOT NULL,
  UNIQUE NULLS NOT DISTINCT (state_code, effective_from, upto_minor)
);

-- EPF/EPS/ESI/TDS: sources checked 2026-11-14. All values: CA review.
INSERT INTO payroll.params (key, effective_from, value, source) VALUES
 ('pf_wage_ceiling_minor', '2025-04-01', 1500000, 'https://lexplosion.in/?p=149380 (ceiling Rs 15,000 retained; Ministry of Labour notice 29 May 2026 as reported)'),
 ('pf_ee_bp', '2025-04-01', 1200, 'EPF Act: 12% of PF wages (https://labourlawreporter.com/epf.asp)'),
 ('pf_er_total_bp', '2025-04-01', 1200, 'EPF Act: employer 12% = EPS + EPF share (https://labourlawreporter.com/epf.asp)'),
 ('eps_bp', '2025-04-01', 833, 'EPS 8.33% of wages up to the ceiling (https://labourlawreporter.com/epf.asp)'),
 ('eps_max_minor', '2025-04-01', 125000, 'EPS capped at Rs 1,250 a month (8.33% of Rs 15,000)'),
 ('wage_rule_exclusion_bp', '2025-11-21', 5000, 'Code on Wages 2019, in force 21 Nov 2025: exclusions above 50% of remuneration count as wages (https://ksandk.com/employment-law/guides/fifty-percent-wage-rule/; https://taxguru.in/corporate-law/employers-wage-rules-effective-21st-nov-2025.html). How the excess feeds PF wages is disputed: CA review'),
 ('esi_ceiling_minor', '2025-04-01', 2100000, 'ESIC: coverage up to Rs 21,000 a month (https://tallysolutions.com/business-guides/esi-calculation-formula-contribution-rate-example/)'),
 ('esi_ee_bp', '2025-04-01', 75, 'ESIC employee 0.75% (https://tallysolutions.com/business-guides/esi-calculation-formula-contribution-rate-example/)'),
 ('esi_er_bp', '2025-04-01', 325, 'ESIC employer 3.25% (https://tallysolutions.com/business-guides/esi-calculation-formula-contribution-rate-example/)'),
 ('tds_std_deduction_minor', '2025-04-01', 7500000, 'New regime standard deduction Rs 75,000, carried into Income-tax Act 2025 (https://taxguru.in/income-tax/guide-tds-tcs-restructuring-fy-2026-27-income-tax-act-2025.html)'),
 ('tds_rebate_limit_minor', '2025-04-01', 120000000, 'Rebate (old s.87A, now s.202 of the 2025 Act) when taxable income is at most Rs 12 lakh (same source)'),
 ('tds_rebate_max_minor', '2025-04-01', 6000000, 'Rebate up to Rs 60,000 (same source)'),
 ('tds_cess_bp', '2025-04-01', 400, 'Health and education cess 4% (same source)'),
 ('tds_surcharge_from_minor', '2025-04-01', 500000000, 'Surcharge starts above Rs 50 lakh; NOT computed by this module, the line is flagged (same source)');

INSERT INTO payroll.tax_slabs (regime, effective_from, from_minor, rate_bp, source)
SELECT 'new', '2025-04-01', f, r, 'https://taxguru.in/income-tax/guide-tds-tcs-restructuring-fy-2026-27-income-tax-act-2025.html (Budget 2026 left slabs unchanged; CA review)'
FROM (VALUES (0,0),(40000000,500),(80000000,1000),(120000000,1500),(160000000,2000),(200000000,2500),(240000000,3000)) v(f, r);

-- Professional tax: https://www.indpayroll.com/blog/professional-tax-slab-rates-by-state-in-india-2026-complete-guide and
-- https://www.bankbazaar.com/tax/karnataka-professional-tax-slab-rate.html. Sources disagree (Karnataka nil limit 15,000 vs 24,999; West Bengal; Telangana
-- middle band): confirm each state on its own portal. Women's exemption (Maharashtra) is not modelled. CA review.
INSERT INTO payroll.pt_slabs (state_code, state_name, effective_from, upto_minor, amount_minor, feb_amount_minor, basis_months, months, source) VALUES
 ('29','Karnataka','2025-04-01', 2499900, 0, NULL, 1, NULL, 'bankbazaar: revised 1 Apr 2025, nil up to Rs 24,999'),
 ('29','Karnataka','2025-04-01', NULL, 20000, 30000, 1, NULL, 'bankbazaar: Rs 200 a month, Rs 300 in February'),
 ('27','Maharashtra','2025-04-01', 750000, 0, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('27','Maharashtra','2025-04-01', 1000000, 17500, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('27','Maharashtra','2025-04-01', NULL, 20000, 30000, 1, NULL, 'indpayroll 2026 guide: Rs 300 in February'),
 ('33','Tamil Nadu','2025-04-01', 2100000, 0, NULL, 6, '{9,3}', 'indpayroll 2026 guide: half-yearly, deducted in Sep and Mar'),
 ('33','Tamil Nadu','2025-04-01', 3000000, 13500, NULL, 6, '{9,3}', 'indpayroll 2026 guide'),
 ('33','Tamil Nadu','2025-04-01', 4500000, 31500, NULL, 6, '{9,3}', 'indpayroll 2026 guide'),
 ('33','Tamil Nadu','2025-04-01', 6000000, 69000, NULL, 6, '{9,3}', 'indpayroll 2026 guide'),
 ('33','Tamil Nadu','2025-04-01', 7500000, 102500, NULL, 6, '{9,3}', 'indpayroll 2026 guide'),
 ('33','Tamil Nadu','2025-04-01', NULL, 125000, NULL, 6, '{9,3}', 'indpayroll 2026 guide'),
 ('36','Telangana','2025-04-01', 1500000, 0, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('36','Telangana','2025-04-01', 2000000, 15000, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('36','Telangana','2025-04-01', NULL, 20000, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('19','West Bengal','2025-04-01', 1000000, 0, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('19','West Bengal','2025-04-01', 1500000, 11000, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('19','West Bengal','2025-04-01', 2500000, 13000, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('19','West Bengal','2025-04-01', 4000000, 15000, NULL, 1, NULL, 'indpayroll 2026 guide'),
 ('19','West Bengal','2025-04-01', NULL, 20000, NULL, 1, NULL, 'indpayroll 2026 guide');

GRANT SELECT ON payroll.params, payroll.tax_slabs, payroll.pt_slabs TO cnote_app;

-- ---------- workspace data ----------
CREATE TABLE payroll.salary_structures (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  employee_id uuid NOT NULL,
  basic_minor bigint NOT NULL CHECK (basic_minor >= 0),
  hra_minor bigint NOT NULL DEFAULT 0 CHECK (hra_minor >= 0),
  special_minor bigint NOT NULL DEFAULT 0 CHECK (special_minor >= 0),
  other_minor bigint NOT NULL DEFAULT 0 CHECK (other_minor >= 0),
  pt_state text CHECK (pt_state ~ '^[0-9]{2}$'),
  pf_applicable boolean NOT NULL DEFAULT true,
  esi_applicable boolean NOT NULL DEFAULT false,
  regime text NOT NULL DEFAULT 'new' CHECK (regime IN ('new','old')),
  pan text CHECK (pan ~ '^[A-Z]{5}[0-9]{4}[A-Z]$'),
  bank_account text CHECK (bank_account ~ '^[0-9]{6,20}$'),
  ifsc text CHECK (ifsc ~ '^[A-Z]{4}0[A-Z0-9]{6}$'),
  uan text CHECK (uan ~ '^[0-9]{12}$'),
  esic_no text CHECK (esic_no ~ '^[0-9]{10,17}$'),
  branch_id uuid,
  redacted_at timestamptz,
  updated_by uuid,
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, employee_id),
  FOREIGN KEY (workspace_id, employee_id) REFERENCES hr.employees (workspace_id, id),
  FOREIGN KEY (workspace_id, branch_id) REFERENCES core.branches (workspace_id, id) ON DELETE SET NULL (branch_id)
);

CREATE TABLE payroll.runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  month date NOT NULL CHECK (month = date_trunc('month', month)::date),
  branch_id uuid,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','approved','finalised','paid','reversed')),
  employees integer NOT NULL DEFAULT 0,
  gross_minor bigint NOT NULL DEFAULT 0,
  deductions_minor bigint NOT NULL DEFAULT 0,
  net_minor bigint NOT NULL DEFAULT 0,
  employer_minor bigint NOT NULL DEFAULT 0,
  warnings jsonb NOT NULL DEFAULT '[]',
  created_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  approved_by uuid, approved_at timestamptz,
  finalised_by uuid, finalised_at timestamptz,
  paid_by uuid, paid_at timestamptz, paid_reference text,
  reversed_by uuid, reversed_at timestamptz, reverse_reason text,
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, branch_id) REFERENCES core.branches (workspace_id, id) ON DELETE SET NULL (branch_id)
);
CREATE INDEX runs_month ON payroll.runs (workspace_id, month);

CREATE TABLE payroll.adjustments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  run_id uuid NOT NULL,
  employee_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('earning','deduction')),
  amount_minor bigint NOT NULL CHECK (amount_minor > 0),
  reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 200),
  created_by uuid,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, id),
  FOREIGN KEY (workspace_id, run_id) REFERENCES payroll.runs (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, employee_id) REFERENCES hr.employees (workspace_id, id)
);

CREATE TABLE payroll.run_lines (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES core.workspaces(id) ON DELETE CASCADE,
  run_id uuid NOT NULL,
  employee_id uuid NOT NULL,
  name text NOT NULL,                 -- snapshots: a payslip never changes after the fact
  code text,
  pan text, bank_account text, ifsc text, uan text, esic_no text, pt_state text,
  days_in_month integer NOT NULL,
  lop_halves integer NOT NULL DEFAULT 0,      -- loss of pay in half days
  unmarked_days integer NOT NULL DEFAULT 0,
  basic_minor bigint NOT NULL, hra_minor bigint NOT NULL, special_minor bigint NOT NULL, other_minor bigint NOT NULL,   -- payable after loss of pay
  adj_earnings_minor bigint NOT NULL DEFAULT 0,
  gross_minor bigint NOT NULL,
  pf_wages_minor bigint NOT NULL DEFAULT 0,
  epf_ee_minor bigint NOT NULL DEFAULT 0,
  esi_wages_minor bigint NOT NULL DEFAULT 0,
  esi_ee_minor bigint NOT NULL DEFAULT 0,
  pt_minor bigint NOT NULL DEFAULT 0,
  tds_minor bigint NOT NULL DEFAULT 0,
  adj_deductions_minor bigint NOT NULL DEFAULT 0,
  deductions_minor bigint NOT NULL,
  net_minor bigint NOT NULL,
  epf_er_minor bigint NOT NULL DEFAULT 0,
  eps_er_minor bigint NOT NULL DEFAULT 0,
  esi_er_minor bigint NOT NULL DEFAULT 0,
  warnings jsonb NOT NULL DEFAULT '[]',
  redacted_at timestamptz,
  UNIQUE (workspace_id, id),
  UNIQUE (workspace_id, run_id, employee_id),
  FOREIGN KEY (workspace_id, run_id) REFERENCES payroll.runs (workspace_id, id) ON DELETE CASCADE,
  FOREIGN KEY (workspace_id, employee_id) REFERENCES hr.employees (workspace_id, id)
);

SELECT core.enable_tenant_rls('payroll.salary_structures');
SELECT core.enable_tenant_rls('payroll.runs');
SELECT core.enable_tenant_rls('payroll.adjustments');
SELECT core.enable_tenant_rls('payroll.run_lines');
GRANT SELECT, INSERT, UPDATE, DELETE ON payroll.salary_structures, payroll.runs, payroll.adjustments, payroll.run_lines TO cnote_app;
