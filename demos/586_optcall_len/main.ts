function main(): i32 {
  console.log("ab12".match(/[0-9]+/)?.length ?? -1);
  return 0;
}
