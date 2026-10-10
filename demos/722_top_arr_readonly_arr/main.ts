const C: readonly Array<i32> = [7];
function main(): i32 {
  console.log(C[0] + C.length);
  console.log(C.concat([8]).length);
  return 0;
}
