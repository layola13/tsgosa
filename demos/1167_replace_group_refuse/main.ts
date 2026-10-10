function main(): i32 {
  console.log("John Smith".replace(/(\w+)\s(\w+)/, "$2 $1"));
  return 0;
}
