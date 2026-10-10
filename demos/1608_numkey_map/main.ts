function main(): i32 {
  const m = new Map<number, string>();
  m.set(1, "one");
  m.set(2, "two");
  console.log(m.get(1) ?? "none");
  console.log(m.size);
  return 0;
}
