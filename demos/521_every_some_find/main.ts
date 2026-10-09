function main(): i32 {
  const e = [1, 2, 3];
  console.log(e.every((x) => x > 0) ? 1 : 0);
  console.log(e.some((x) => x > 2) ? 1 : 0);
  console.log(e.find((x) => x > 1) ?? -1);
  return 0;
}
