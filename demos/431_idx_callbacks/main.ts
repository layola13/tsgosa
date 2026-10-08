function main(): i32 {
  const a = [1, 2, 3];
  console.log(a.findLastIndex((x) => x > 1));
  const b = [5, 6].map((x, i) => x + i);
  console.log(b[1]);
  const c = [5, 6, 7].filter((x, i) => i > 0);
  console.log(c.length);
  return 0;
}
