import { mount, unmount } from "svelte";
import Speaker from "./Speaker.svelte";

export function mountSpeaker(target) {
  const component = mount(Speaker, { target });
  return { dispose: () => unmount(component) };
}
