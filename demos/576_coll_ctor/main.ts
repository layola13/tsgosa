function main(): i32 {
  const s = new Set([1, 2, 2, 3]);
  console.log(s.size);
  console.log(s.has(2) ? 1 : 0);
  const m = new Map([["a", 1]]);
  console.log(m.get("a"));
  return 0;
}
