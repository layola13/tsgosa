function one(): i32 { return 1; }
function two(): i32 { return one() + one(); }
function main(): i32 {
  console.log(two());
  return 0;
}
