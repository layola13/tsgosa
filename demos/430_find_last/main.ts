function main(): i32 {
  const a = [1, 2, 3];
  const v = a.findLast((x) => x > 1);
  console.log(v === undefined ? 0 : v);
  const w = a.findLast((x) => x > 9);
  console.log(w === undefined ? 0 : w);
  return 0;
}
