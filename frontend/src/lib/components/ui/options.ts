/**
 * The candidate option shape shared by dropdown components.
 *
 * Select and Combobox use the same field names, so callers need not change the data shape when switching controls; it lives in its own module rather than
 * hanging off a component so that non-component modules can also `import type` it (that is how the timezone candidates use it).
 */
export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}
