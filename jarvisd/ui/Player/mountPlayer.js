import { mount, unmount } from "svelte";
import Player from "./Player.svelte";

export function mountPlayer(target) {
  const component = mount(Player, { target });
  return { dispose: () => unmount(component) };
}
