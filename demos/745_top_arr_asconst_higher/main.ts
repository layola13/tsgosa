const C = [2, 3] as const;
function main(): i32 {
  console.log(C.map((x) => x * 2)[0] + C.filter((x) => x > 2)[0]);
  return 0;
}
