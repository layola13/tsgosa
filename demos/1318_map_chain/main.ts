function main(): i32 {
  const m = new Map<string, i32>();
  console.log(m.set("a", 1).get("a")!);
  return 0;
}
