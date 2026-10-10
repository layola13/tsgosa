function main(): i32 {
  const m = new Map<string, i32>();
  m.set("a", 1); m.set("b", 2); m.set("c", 3);
  m.delete("b");
  console.log(m.size);
  console.log(m.get("c") ?? -1);
  return 0;
}
