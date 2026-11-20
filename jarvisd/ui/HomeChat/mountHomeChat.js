import { mount, unmount } from "svelte";
import HomeChat from "./HomeChat.svelte";

export function mountHomeChat(target) {
  const component = mount(HomeChat, { target });
  return {
    refresh: component.refresh,
    dispose: () => unmount(component),
  };
}
