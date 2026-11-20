import { mount, unmount } from "svelte";
import Charts from "./Charts.svelte";

export function mountCharts(target) {
  const component = mount(Charts, { target });
  return {
    refresh: component.refresh,
    dispose: () => unmount(component),
  };
}
