import { mount, unmount } from "svelte";
import Security from "./Security.svelte";

export function mountSecurity(target) {
  const component = mount(Security, { target });
  return { dispose: () => unmount(component) };
}
