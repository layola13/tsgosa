function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1);
  console.log(m.has("a") && m.get("a") == 1 ? 1 : 0);
  return 0;
}
