function main(): i32 {
  const s = [10, 20, 30].reduce((a, b, i) => a + b + i, 0);
  console.log(s);
  console.log([5, 6].some((x, i) => x + i > 6) ? 1 : 0);
  return 0;
}
