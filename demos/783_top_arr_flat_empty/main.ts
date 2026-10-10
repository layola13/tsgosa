const E: i32[] = [];
function main(): i32 {
  console.log(E.flatMap((x) => [x]).length);
  console.log(E.findLast((x) => x > 0));
  return 0;
}
