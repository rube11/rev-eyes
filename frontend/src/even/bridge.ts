import { waitForEvenAppBridge } from "@evenrealities/even_hub_sdk";

export async function getEvenBridge() {
  return await waitForEvenAppBridge();
}
