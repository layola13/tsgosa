const M = [-5, 6] as const;
function main(): i32 {
  console.log(M[0] + M.length);
  console.log(M.includes(-5));
  return 0;
}
