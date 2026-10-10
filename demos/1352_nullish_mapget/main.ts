function main(): i32 {
  const m = new Map<string, i32>();
  m.set("k", 11);
  console.log(m.get("z") ?? 42);
  console.log(m.get("k") ?? 42);
  return 0;
}
