const C = [[5]] as const;
function main(): i32 {
  console.log(C[0][0]);
  console.log(C.length);
  return 0;
}
