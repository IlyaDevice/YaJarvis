import { mount, unmount } from "svelte";
import Training from "./Training.svelte";

export function mountTraining(target, showRecords) {
  const component = mount(Training, { target, props: { showRecords } });
  return { dispose: () => unmount(component) };
}
