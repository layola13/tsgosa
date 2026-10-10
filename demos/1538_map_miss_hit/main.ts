function main(): i32 {
  const m = new Map<string, i32>();
  console.log(m.get("k") ?? 100);
  m.set("k", 1);
  console.log(m.get("k") ?? 100);
  return 0;
}
