function main(): i32 {
  const m = new Map<string, string>();
  m.set("k1", "v1");
  m.set("k2", "v2");
  console.log(m.size);
  console.log(m.get("k2") ?? "none");
  return 0;
}
