function main(): i32 {
  const m = new Map<string, i32>();
  console.log(m.set("a", 1).delete("a") ? 1 : 0);
  console.log(m.has("a") ? 1 : 0);
  return 0;
}
