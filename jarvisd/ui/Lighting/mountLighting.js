import { mount, unmount } from "svelte";
import Lighting from "./Lighting.svelte";

export function mountLighting(target) {
  const component = mount(Lighting, { target });
  return { dispose: () => unmount(component) };
}
