import type { Spec } from './lib'

export type Template = {
  id: string
  title: string
  blurb: string
  category: 'Money' | 'Sales' | 'Projects' | 'Support'
  spec: Spec
}

const bar = (dataset: string, group: string, m: Spec['measures'], rest: Partial<Spec> = {}): Spec => ({
  dataset,
  filters: [],
  group_by: [{ field: group }],
  measures: m,
  sort: [{ key: 'm0', dir: 'desc' }],
  viz: 'bar',
  ...rest,
})

export const TEMPLATES: Template[] = [
  {
    id: 'owed',
    title: 'Who owes me money?',
    blurb: 'Unpaid invoices by customer',
    category: 'Money',
    spec: bar('invoices', 'contact', [{ field: 'balance', agg: 'sum' }], {
      filters: [{ field: 'status', op: 'in', value: ['issued', 'irn_generated', 'sent', 'partly_paid'] }],
    }),
  },
  {
    id: 'top-customers',
    title: 'Who are my top customers?',
    blurb: 'Invoiced amount by customer, this financial year',
    category: 'Money',
    spec: bar('invoices', 'contact', [{ field: 'total', agg: 'sum' }], {
      filters: [
        { field: 'kind', op: 'in', value: ['invoice'] },
        { field: 'status', op: 'not_in', value: ['draft', 'cancelled'] },
      ],
      period: { field: 'invoice_date', preset: 'this_fy' },
    }),
  },
  {
    id: 'time',
    title: 'Where did our time go?',
    blurb: 'Hours by project, this month',
    category: 'Projects',
    spec: bar('time_entries', 'project', [{ field: 'hours', agg: 'sum' }], {
      period: { field: 'worked_on', preset: 'this_month' },
    }),
  },
  {
    id: 'pipeline',
    title: 'What is in my pipeline?',
    blurb: 'Open deals by stage',
    category: 'Sales',
    spec: bar('deals', 'stage', [{ field: 'amount', agg: 'sum' }, { agg: 'count' }], {
      filters: [{ field: 'stage', op: 'not_in', value: ['won', 'lost'] }],
      viz: 'table',
    }),
  },
  {
    id: 'revenue-month',
    title: 'Revenue by month',
    blurb: 'Invoiced amount per month, this financial year',
    category: 'Money',
    spec: {
      ...bar('invoices', 'invoice_date', [{ field: 'total', agg: 'sum' }], {
        filters: [
          { field: 'kind', op: 'in', value: ['invoice'] },
          { field: 'status', op: 'not_in', value: ['draft', 'cancelled'] },
        ],
        period: { field: 'invoice_date', preset: 'this_fy' },
      }),
      group_by: [{ field: 'invoice_date', bucket: 'month' }],
      sort: [{ key: 'g0', dir: 'asc' }],
    },
  },
  {
    id: 'spending',
    title: 'What are we spending on?',
    blurb: 'Expenses by category, this financial year',
    category: 'Money',
    spec: bar('expenses', 'category', [{ field: 'amount', agg: 'sum' }], {
      period: { field: 'spent_on', preset: 'this_fy' },
    }),
  },
  {
    id: 'tickets',
    title: 'Open tickets by priority',
    blurb: 'Tickets not yet resolved',
    category: 'Support',
    spec: bar('tickets', 'priority', [{ agg: 'count' }], {
      filters: [{ field: 'status', op: 'not_in', value: ['resolved', 'closed'] }],
      viz: 'table',
    }),
  },
  {
    id: 'overdue-tasks',
    title: 'Overdue tasks by person',
    blurb: 'Open tasks past their due date',
    category: 'Projects',
    spec: bar('tasks', 'assignee', [{ agg: 'count' }], {
      filters: [
        { field: 'due_date', op: 'before', value: 'today' },
        { field: 'status_category', op: 'not_in', value: ['done'] },
      ],
    }),
  },
]
