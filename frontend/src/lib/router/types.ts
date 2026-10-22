import type { Component } from 'svelte';

/**
 * Route definition interface
 */
export interface RouteDefinition {
  path: string;
  name: string;
  component: Component<Record<string, unknown>>;
}

/**
 * Current route match information
 */
export interface RouteMatch {
  path: string;
  params: Record<string, string>;
  query: Record<string, string>;
  /**
   * Raw query string (without the leading '?', duplicate keys preserved). query is the parsed object, which keeps only the last value of a duplicate key;
   * use this raw value for judgments where "the URL is the view identity" (e.g. rebuilding the view per route), not query.
   */
  search: string;
  route: RouteDefinition | null;
}
