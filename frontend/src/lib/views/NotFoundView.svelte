<script lang="ts">
  import { t } from '../i18n';
</script>

<div class="py-16 max-w-md mx-auto px-4 text-center">
  <div class="card-elevated p-8 rounded-xl">
    <div class="text-4xl font-bold text-zinc-400 mb-2">404</div>
    <h1 class="text-xl font-bold text-zinc-900 dark:text-zinc-100 mb-4">
      {$t('common.not_found')}
    </h1>
    <a
      href="/"
      class="inline-flex items-center px-4 py-2 border border-transparent text-sm font-medium rounded-md shadow-xs text-white bg-zinc-900 hover:bg-zinc-800 dark:bg-zinc-100 dark:text-zinc-900 dark:hover:bg-zinc-200 btn-press"
    >
      {$t('common.back_home')}
    </a>
  </div>
</div>
