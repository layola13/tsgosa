export function main(): i32 {
  console.log("a".localeCompare("b") < 0 ? 1 : 0);
  console.log("b".localeCompare("a") > 0 ? 1 : 0);
  console.log("a".localeCompare("a") === 0 ? 1 : 0);
  return 0;
}
