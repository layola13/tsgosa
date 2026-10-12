function main(): i32 {
  const m = new Map<string, number>();
  m.set("a", 1);
  const f: boolean = false;
  console.log(f == m["a"]);
  console.log(f != m["zz"]);
  return 0;
}
