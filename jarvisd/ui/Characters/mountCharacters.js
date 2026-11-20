import { mount, unmount } from "svelte";
import Characters from "./Characters.svelte";

export function mountCharacters(target) {
  const component = mount(Characters, { target });
  return { dispose: () => unmount(component) };
}
