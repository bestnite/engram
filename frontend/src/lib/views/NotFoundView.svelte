<script lang="ts">
  import Page from '../components/ui/Page.svelte';
  import { t } from '../i18n';
  import Button from '../components/ui/Button.svelte';
</script>

<Page width="narrow" class="text-center">
  <div>
    <div class="text-4xl font-bold text-zinc-400 mb-2">404</div>
    <h1 class="text-xl font-bold text-foreground mb-4">
      {$t('common.not_found')}
    </h1>
    <Button href="/" variant="primary" size="lg" testId="not-found-home">
      {$t('common.back_home')}
    </Button>
  </div>
</Page>
