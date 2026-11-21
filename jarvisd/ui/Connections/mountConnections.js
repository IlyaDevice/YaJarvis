import { mount, unmount } from "svelte";
import Connections from "./Connections.svelte";

export function mountConnections(target, openKey) {
  const component = mount(Connections, { target, props: { openKey } });
  return { dispose: () => unmount(component) };
}
