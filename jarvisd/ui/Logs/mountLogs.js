import { mount, unmount } from "svelte";
import Logs from "./Logs.svelte";

export function mountLogs(target) {
  const component = mount(Logs, { target });
  return {
    refresh: component.refresh,
    dispose: () => unmount(component),
  };
}
