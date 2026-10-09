<script lang="ts">
  import { onMount } from 'svelte';
  import { navigate } from '../../router';

  /**
   * /admin 不再有概览页：原来的计数卡片只是把各子页的数字重复一遍，没有提供可操作的信息。
   * 这条路由保留下来，让侧边栏之外的旧链接与书签不作废——它不渲染内容，只把地址换成
   * 管理导航的第一项（用户管理）。
   */
  onMount(() => {
    navigate('/admin/users', true);
  });
</script>
