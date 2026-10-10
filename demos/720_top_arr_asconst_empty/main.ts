const E = [] as const;
function main(): i32 {
  console.log(E.length);
  console.log(E.slice().length);
  return 0;
}
