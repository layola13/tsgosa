function main(): i32 {
  Deno.mkdir("/tmp/tsgosa_deno_208");
  Deno.remove("/tmp/tsgosa_deno_208");
  console.log(btoa("ok").length);
  return 0;
}