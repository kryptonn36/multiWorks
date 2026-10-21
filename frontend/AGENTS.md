# Frontend purpose

This frontend is for testing the existing backend through normal forms and lists.

- Treat backend handlers as the source of truth for request fields and API contracts. Inspect them when backend changes are mentioned.
- Leave business validation and authorization to the backend. Do not add browser validation (`required`, length limits, UUID/email patterns), JavaScript business validation, or role-based hiding/disabling of submission actions. Use `novalidate` on forms.
- Inputs and role/member suggestions must allow arbitrary values, including blank or invalid ones, so backend constraints can be tested. Do not silently trim or normalize submitted values.
- Do not automatically change a test's prerequisites (such as adding workspace membership before a project request); let the backend return its result.
- Preserve token handling, safe HTML escaping, route encoding, loading states, duplicate-submit prevention, and visible backend errors. These are application mechanics, not replacements for backend constraints.
