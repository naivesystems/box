namespace :redmine do
  task :export_admin_api_key => :environment do
    admin = User.where(login: 'admin').first
    if admin.api_token
      admin.api_token.destroy
      admin.reload
    end
    puts admin.api_key
    admin.save!
    Setting.rest_api_enabled = '1'
  end
end
