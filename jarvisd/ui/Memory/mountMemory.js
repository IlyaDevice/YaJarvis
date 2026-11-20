import { mount, unmount } from "svelte";
import Memory from "./Memory.svelte";

export function mountMemory(target) {
  const component = mount(Memory, { target });
  return { dispose: () => unmount(component) };
}
