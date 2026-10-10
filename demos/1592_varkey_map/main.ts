function main(): i32 {
  const o: string = "key";
  const m = new Map<string, i32>();
  m.set(o, 5);
  console.log(m.get(o) ?? -1);
  return 0;
}
