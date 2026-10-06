import type { RouteDefinition } from './types';
import HomeView from '../views/HomeView.svelte';
import DecksView from '../views/DecksView.svelte';
import DeckDetailView from '../views/DeckDetailView.svelte';
import StatsView from '../views/StatsView.svelte';
import SettingsView from '../views/SettingsView.svelte';
import APIKeysView from '../views/APIKeysView.svelte';
import LoginView from '../views/LoginView.svelte';
import ReviewView from '../views/ReviewView.svelte';
import NoteEditView from '../views/NoteEditView.svelte';
import NoteCreateView from '../views/NoteCreateView.svelte';
import ImportView from '../views/ImportView.svelte';

/**
 * 前端骨架路由定义列表（DESIGN.md §8.1）
 */
export const routes: RouteDefinition[] = [
  {
    path: '/import',
    name: 'import',
    component: ImportView as unknown as RouteDefinition['component'],
  },
  {
    path: '/',
    name: 'home',
    component: HomeView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks',
    name: 'decks',
    component: DecksView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id',
    name: 'deck-detail',
    component: DeckDetailView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes',
    name: 'deck-notes',
    component: DeckDetailView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes/:noteId/edit',
    name: 'note-edit',
    component: NoteEditView as unknown as RouteDefinition['component'],
  },
  {
    path: '/decks/:id/notes/new',
    name: 'note-create',
    component: NoteCreateView as unknown as RouteDefinition['component'],
  },
  {
    path: '/spa/review',
    name: 'review',
    component: ReviewView as unknown as RouteDefinition['component'],
  },
  {
    path: '/stats',
    name: 'stats',
    component: StatsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/settings',
    name: 'settings',
    component: SettingsView as unknown as RouteDefinition['component'],
  },
  {
    path: '/settings/keys',
    name: 'settings-keys',
    component: APIKeysView as unknown as RouteDefinition['component'],
  },
  {
    path: '/login',
    name: 'login',
    component: LoginView as unknown as RouteDefinition['component'],
  },
];
