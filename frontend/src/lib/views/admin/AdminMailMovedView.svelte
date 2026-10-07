<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from '../../router';

  /**
   * 邮件模板已并入「邮件」页（/admin/smtp）：配好发信通道之后紧接着就是「发出去的信长什么样」，
   * 分成两个 tab 只会让人来回点。
   *
   * 这条路由只为了让旧链接与书签不作废——它不渲染内容，只把地址换过去，因此浏览器里看到的
   * 是那一页而不是一个副本。
   */
  onMount(() => {
    navigate('/admin/smtp', true);
  });
</script>
