function main(): i32 {
  const m = new Map<string, string>();
  m.set("k", "hi");
  console.log(m.get("k") ?? "miss");
  console.log(m.get("z") ?? "miss");
  return 0;
}
