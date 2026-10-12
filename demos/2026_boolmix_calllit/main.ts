function getN(): number { return 1; }
function main(): i32 {
  console.log(true == getN());
  console.log(false != getN());
  return 0;
}
