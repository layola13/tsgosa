const C = [2, 3] as const;
function main(): i32 {
  console.log(C.flatMap((x) => [x, x + 1]).length);
  console.log(C.findLast((x) => x > 2));
  return 0;
}
